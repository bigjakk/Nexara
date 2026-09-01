package virtiowin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// MirrorSettingKey is the global settings key holding the mirror base URL.
//
// Instance-wide rather than per-cluster, because the release catalog it feeds
// is instance-wide: virtio_win_releases has one row per version for the whole
// install, so a per-cluster base would leave iso_url wrong for every cluster
// but one. Being cut off from fedorapeople.org is also a property of the
// install, not of a cluster.
//
// internal/api/handlers declares this literal a second time so
// TestGuard_GlobalSettingKeysClassified can resolve it statically; the two are
// held together by TestVirtioWinMirrorSettingKeyMatches there.
const MirrorSettingKey = "virtio_win.mirror"

// Mirror is the stored shape of that setting.
type Mirror struct {
	// BaseURL replaces the upstream download root. Empty means fedorapeople.
	BaseURL string `json:"base_url"`
}

// NormalizeBase trims a base URL to the form the URL builders expect: no
// trailing slash, no query, no fragment. Returns an error for anything that
// cannot be a base — the result is handed to a Proxmox node to fetch.
func NormalizeBase(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("virtiowin: mirror URL does not parse: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("virtiowin: mirror URL must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("virtiowin: mirror URL must include a host")
	}
	if u.User != nil {
		return "", errors.New("virtiowin: mirror URL must not contain credentials")
	}
	// A query or fragment on a base cannot survive having a path appended to
	// it, so silently keeping either would build URLs that 404 with no clue why.
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("virtiowin: mirror URL must not carry a query or fragment")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String(), nil
}

// ResolveBase returns the download root to use: the configured mirror when one
// is set, otherwise upstream.
//
// A malformed or unreadable setting falls back to upstream rather than
// failing the check. The value is validated on write, so reaching that branch
// means the row was hand-edited; an install that can still reach upstream
// should keep working rather than stop on a bad override.
func (e *Engine) ResolveBase(ctx context.Context) string {
	row, err := e.queries.GetSetting(ctx, db.GetSettingParams{
		Key:     MirrorSettingKey,
		Scope:   "global",
		ScopeID: pgtype.UUID{},
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			e.logger.Warn("virtio-win: read mirror setting failed; using upstream", "error", err)
		}
		return BaseURL
	}
	var mirror Mirror
	if err := json.Unmarshal(row.Value, &mirror); err != nil {
		e.logger.Warn("virtio-win: mirror setting is not readable; using upstream", "error", err)
		return BaseURL
	}
	base, err := NormalizeBase(mirror.BaseURL)
	if err != nil || base == "" {
		if err != nil {
			e.logger.Warn("virtio-win: mirror setting is not a usable base URL; using upstream", "error", err)
		}
		return BaseURL
	}
	return base
}

// client returns the upstream client pointed at the currently configured base.
func (e *Engine) client(ctx context.Context) *Client {
	return e.upstream.WithBase(e.ResolveBase(ctx))
}
