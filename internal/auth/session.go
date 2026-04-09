package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// DeviceInfo describes the client device a session is being created for.
// All fields are optional; a zero-value DeviceInfo creates an un-tagged session
// (matching the legacy behavior).
type DeviceInfo struct {
	Name string // human-friendly name (e.g. "Pixel 8 Pro", "Chrome on macOS")
	Type string // one of: "web", "mobile", "desktop" (empty = untagged)
	ID   string // stable per-device identifier (mobile only)
}

func nullText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// SessionManager handles session lifecycle in Redis + PostgreSQL.
type SessionManager struct {
	queries *db.Queries
	redis   *redis.Client
}

// redisSession is the JSON structure stored in Redis for fast session lookup.
type redisSession struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	TokenHash string `json:"token_hash"`
	Role      string `json:"role"`
}

// NewSessionManager creates a new session manager.
func NewSessionManager(queries *db.Queries, rdb *redis.Client) *SessionManager {
	return &SessionManager{
		queries: queries,
		redis:   rdb,
	}
}

// HashToken returns the SHA-256 hex digest of a refresh token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func redisKey(sessionID string) string {
	return "nexara:session:" + sessionID
}

// CreateSession stores a new session in both PostgreSQL and Redis.
func (sm *SessionManager) CreateSession(ctx context.Context, userID uuid.UUID, refreshToken, role, userAgent, ipAddress string, ttl time.Duration, device DeviceInfo) (db.Session, error) {
	tokenHash := HashToken(refreshToken)
	expiresAt := time.Now().Add(ttl)

	session, err := sm.queries.CreateSession(ctx, db.CreateSessionParams{
		UserID:     userID,
		TokenHash:  tokenHash,
		UserAgent:  userAgent,
		IpAddress:  ipAddress,
		ExpiresAt:  expiresAt,
		DeviceName: nullText(device.Name),
		DeviceType: nullText(device.Type),
		DeviceID:   nullText(device.ID),
	})
	if err != nil {
		return db.Session{}, fmt.Errorf("creating session in db: %w", err)
	}

	rs := redisSession{
		SessionID: session.ID.String(),
		UserID:    userID.String(),
		TokenHash: tokenHash,
		Role:      role,
	}
	data, err := json.Marshal(rs)
	if err != nil {
		return db.Session{}, fmt.Errorf("marshaling redis session: %w", err)
	}

	if err := sm.redis.Set(ctx, redisKey(session.ID.String()), data, ttl).Err(); err != nil {
		return db.Session{}, fmt.Errorf("storing session in redis: %w", err)
	}

	return session, nil
}

// ValidateRefreshToken looks up a session by refresh token hash in PostgreSQL.
// Returns the session if valid and not expired/revoked.
func (sm *SessionManager) ValidateRefreshToken(ctx context.Context, refreshToken string) (db.Session, error) {
	tokenHash := HashToken(refreshToken)

	session, err := sm.queries.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return db.Session{}, ErrInvalidToken
	}

	if session.IsRevoked {
		return db.Session{}, ErrInvalidToken
	}

	if time.Now().After(session.ExpiresAt) {
		return db.Session{}, ErrInvalidToken
	}

	return session, nil
}

// RotateRefreshToken replaces the token hash for a session (single-use rotation).
func (sm *SessionManager) RotateRefreshToken(ctx context.Context, sessionID uuid.UUID, newRefreshToken, role string, ttl time.Duration) error {
	newHash := HashToken(newRefreshToken)

	if err := sm.queries.UpdateSessionTokenHash(ctx, db.UpdateSessionTokenHashParams{
		ID:        sessionID,
		TokenHash: newHash,
	}); err != nil {
		return fmt.Errorf("rotating token in db: %w", err)
	}

	// Update Redis
	session, err := sm.queries.GetSessionByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("fetching session after rotation: %w", err)
	}

	rs := redisSession{
		SessionID: sessionID.String(),
		UserID:    session.UserID.String(),
		TokenHash: newHash,
		Role:      role,
	}
	data, err := json.Marshal(rs)
	if err != nil {
		return fmt.Errorf("marshaling redis session: %w", err)
	}

	if err := sm.redis.Set(ctx, redisKey(sessionID.String()), data, ttl).Err(); err != nil {
		return fmt.Errorf("updating session in redis: %w", err)
	}

	return nil
}

// RevokeSession marks a session as revoked in both PostgreSQL and Redis.
func (sm *SessionManager) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := sm.queries.RevokeSession(ctx, sessionID); err != nil {
		return fmt.Errorf("revoking session in db: %w", err)
	}
	sm.redis.Del(ctx, redisKey(sessionID.String()))
	return nil
}

// RevokeAllUserSessions revokes all sessions for a user.
func (sm *SessionManager) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	// Get active sessions to clean up Redis keys
	sessions, err := sm.queries.ListUserSessions(ctx, userID)
	if err != nil {
		return fmt.Errorf("listing user sessions: %w", err)
	}

	if err := sm.queries.RevokeAllUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("revoking all sessions in db: %w", err)
	}

	for _, s := range sessions {
		sm.redis.Del(ctx, redisKey(s.ID.String()))
	}

	return nil
}
