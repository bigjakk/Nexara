package veeam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Access tokens live 900s. Refreshing 120s early keeps a long-running sync
// from crossing the boundary mid-fan-out, without halving the token's useful
// life the way an aggressive lead time would.
const refreshLead = 120 * time.Second

// tokenPath and logoutPath are unversioned — the OAuth2 endpoints sit outside
// /api/v1, though they still honour (and are still sent) x-api-version.
const (
	tokenPath  = "/api/oauth2/token"
	logoutPath = "/api/oauth2/logout"
)

// cachedToken is one cached credential pair.
type cachedToken struct {
	access    string
	refresh   string
	expiresAt time.Time
}

func (t cachedToken) valid(now time.Time) bool {
	return t.access != "" && now.Before(t.expiresAt.Add(-refreshLead))
}

// accessToken returns a usable bearer token, minting or refreshing one if the
// cached value is missing or close to expiry.
//
// Concurrent callers collapse onto a single in-flight grant via singleflight.
// Without that, a sync fan-out across a dozen endpoints mints a dozen tokens
// on the first tick — VBR happily issues them, and every one is a separate
// session on the server.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokMu.Lock()
	cached := c.tok
	c.tokMu.Unlock()
	if cached.valid(time.Now()) {
		return cached.access, nil
	}

	result, err, _ := c.group.Do("token", func() (any, error) {
		// Re-check inside the flight: the goroutine that won the race may
		// have refreshed while this one was queued behind it.
		c.tokMu.Lock()
		cached := c.tok
		c.tokMu.Unlock()
		if cached.valid(time.Now()) {
			return cached.access, nil
		}

		// Detached from the caller's cancellation, with its own deadline.
		//
		// singleflight hands one flight's error to every goroutine that
		// joined it, so a flight bound to the first caller's context fails
		// every other caller the moment that one caller times out or goes
		// away — even those with minutes of budget left. The token being
		// minted is shared state; it should not belong to whoever happened
		// to ask for it first. Values are preserved so tracing and deadline
		// -free request metadata still ride along.
		grantCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()

		fresh, err := c.grant(grantCtx, cached)
		if err != nil {
			return "", err
		}

		c.tokMu.Lock()
		c.tok = fresh
		c.tokMu.Unlock()
		return fresh.access, nil
	})
	if err != nil {
		return "", err
	}

	access, _ := result.(string)
	return access, nil
}

// grant obtains a fresh token, preferring the refresh grant when a refresh
// token is on hand.
//
// A failed refresh falls through to a password grant rather than surfacing:
// refresh tokens expire, are invalidated by a logout elsewhere, and do not
// survive a VBR restart, and in every one of those cases the password we hold
// still works. An auth failure from the password grant is real and propagates.
func (c *Client) grant(ctx context.Context, cached cachedToken) (cachedToken, error) {
	if cached.refresh != "" {
		refreshed, err := c.requestToken(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {cached.refresh},
		})
		if err == nil {
			return refreshed, nil
		}
		// A refresh failing on an unreachable server is not a credential
		// problem, and retrying with the password would only produce the
		// same transport error with a worse message.
		if errors.Is(err, ErrUnreachable) {
			return cachedToken{}, err
		}
	}

	return c.requestToken(ctx, url.Values{
		"grant_type": {"password"},
		"username":   {c.username},
		"password":   {c.password},
	})
}

// requestToken performs one OAuth2 grant.
//
// url.Values.Encode percent-encodes the form body, which is what keeps a
// domain-qualified `DOMAIN\user` intact: the backslash goes out as %5C and VBR
// decodes it back. The credential never touches a shell, a URL path or a log
// line anywhere in this function.
func (c *Client) requestToken(ctx context.Context, form url.Values) (cachedToken, error) {
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+tokenPath, strings.NewReader(body))
	if err != nil {
		return cachedToken{}, fmt.Errorf("veeam: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	c.setRevisionHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return cachedToken{}, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return cachedToken{}, fmt.Errorf("veeam: read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := decodeAPIError(resp.StatusCode, raw)
		if resp.StatusCode == http.StatusUnauthorized {
			return cachedToken{}, authFailure(c.username, apiErr)
		}
		return cachedToken{}, apiErr
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return cachedToken{}, fmt.Errorf("veeam: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return cachedToken{}, fmt.Errorf("veeam: token response carried no access_token")
	}

	// Treat a missing or nonsensical expires_in as a short-lived token rather
	// than an eternal one: worst case we refresh more often than needed.
	lifetime := time.Duration(tr.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 15 * time.Minute
	}

	return cachedToken{
		access:    tr.AccessToken,
		refresh:   tr.RefreshToken,
		expiresAt: time.Now().Add(lifetime),
	}, nil
}

// invalidate drops the cached token, but only if it is still the one the
// caller saw fail. A concurrent refresh that already succeeded must not be
// thrown away by a 401 from a request that was in flight before it.
func (c *Client) invalidate(stale string) {
	c.tokMu.Lock()
	defer c.tokMu.Unlock()
	if c.tok.access == stale {
		c.tok = cachedToken{}
	}
}

// Logout invalidates the cached access and refresh tokens on the server and
// clears them locally. Safe to call when no token was ever minted.
//
// Best-effort by design: the tokens expire on their own in 15 minutes, so a
// failed logout is not worth surfacing to an operator who just successfully
// tested a connection.
func (c *Client) Logout(ctx context.Context) error {
	c.tokMu.Lock()
	access := c.tok.access
	c.tok = cachedToken{}
	c.tokMu.Unlock()

	if access == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+logoutPath, nil)
	if err != nil {
		return fmt.Errorf("veeam: build logout request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Length", "0")
	c.setRevisionHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))

	if resp.StatusCode >= http.StatusBadRequest {
		return &APIError{StatusCode: resp.StatusCode, Message: "logout rejected"}
	}
	return nil
}
