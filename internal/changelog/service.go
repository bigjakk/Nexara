package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepo      = "bigjakk/Nexara"
	defaultUserAgent = "nexara-changelog/1.0"
	fetchTimeout     = 10 * time.Second
	maxResponseBytes = 2 * 1024 * 1024
	cacheTTL         = 1 * time.Hour
	errorBackoff     = 5 * time.Minute
	perPage          = 12
)

// Service fetches release notes from GitHub and caches them in memory.
type Service struct {
	repo       string
	httpClient *http.Client
	logger     *slog.Logger

	mu       sync.Mutex
	cache    []Entry
	etag     string
	cachedAt time.Time
	lastErr  time.Time
}

// New returns a Service for the given owner/repo (e.g. "bigjakk/Nexara").
// If repo is empty, defaults to the upstream Nexara repository.
func New(repo string, logger *slog.Logger) *Service {
	if repo == "" {
		repo = defaultRepo
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:       repo,
		httpClient: &http.Client{Timeout: fetchTimeout},
		logger:     logger,
	}
}

// Get returns the cached entries, refreshing from GitHub if the cache has
// expired. On fetch failure, returns the last successful cache (even if
// stale) rather than erroring; first-time failures return an error.
func (s *Service) Get(ctx context.Context) ([]Entry, error) {
	s.mu.Lock()
	cache := s.cache
	cachedAt := s.cachedAt
	lastErr := s.lastErr
	etag := s.etag
	s.mu.Unlock()

	if !cachedAt.IsZero() && time.Since(cachedAt) < cacheTTL {
		return cache, nil
	}
	if !lastErr.IsZero() && time.Since(lastErr) < errorBackoff {
		if len(cache) > 0 {
			return cache, nil
		}
		return nil, errors.New("changelog: temporarily unavailable")
	}

	entries, newEtag, notModified, err := s.fetch(ctx, etag)
	if err != nil {
		s.mu.Lock()
		s.lastErr = time.Now()
		s.mu.Unlock()
		s.logger.Warn("changelog fetch failed", "err", err, "repo", s.repo)
		if len(cache) > 0 {
			return cache, nil
		}
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if notModified {
		s.cachedAt = time.Now()
		s.lastErr = time.Time{}
		return s.cache, nil
	}
	s.cache = entries
	s.etag = newEtag
	s.cachedAt = time.Now()
	s.lastErr = time.Time{}
	return entries, nil
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

func (s *Service) fetch(ctx context.Context, etag string) (entries []Entry, newETag string, notModified bool, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d", s.repo, perPage)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("github releases: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, "", false, fmt.Errorf("read body: %w", err)
	}

	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, "", false, fmt.Errorf("decode releases: %w", err)
	}

	entries = make([]Entry, 0, len(releases))
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		version := strings.TrimPrefix(r.TagName, "v")
		if version == "" {
			continue
		}
		highlights := ParseBody(r.Body)
		if len(highlights) == 0 {
			continue
		}
		entries = append(entries, Entry{
			Version:    version,
			Date:       formatDate(r.PublishedAt),
			Highlights: highlights,
			URL:        r.HTMLURL,
		})
	}

	return entries, resp.Header.Get("ETag"), false, nil
}

func formatDate(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}
