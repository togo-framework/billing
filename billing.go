// Package billing is togo's usage-based billing + API-key plugin. It manages
// per-user API keys (hashed, scoped, revocable), meters usage (especially AI
// token usage) per key/user, tracks consumption, enforces quotas, and produces
// usage reports. The `ai` plugin reports token Usage here via billing.Record.
//
// Install: `togo install togo-framework/billing` (blank-import registers it).
package billing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/togo-framework/orm"
	"github.com/togo-framework/togo"
)

// ctxKey is the context key holding the authenticated APIKey.
type ctxKey struct{}

func init() {
	togo.RegisterProviderFunc("billing", togo.PriorityLate+10, func(k *togo.Kernel) error {
		s := &Service{k: k}
		if err := s.migrate(context.Background()); err != nil {
			return err
		}
		s.routes()
		k.Set("billing", s)
		return nil
	})
}

// Service is the billing service stored in the kernel container under "billing".
type Service struct{ k *togo.Kernel }

func (s *Service) db(ctx context.Context) (*sql.DB, error) { return s.k.SQL(ctx) }

// ── Models ──────────────────────────────────────────────────────────────────

// APIKey is a per-user credential. The plaintext is shown once at creation; only
// its SHA-256 hash is stored.
type APIKey struct {
	ID         string `db:"id" json:"id"`
	UserID     string `db:"user_id" json:"user_id"`
	Name       string `db:"name" json:"name"`
	Prefix     string `db:"prefix" json:"prefix"`
	KeyHash    string `db:"key_hash" json:"-"`
	Scopes     string `db:"scopes" json:"scopes"`
	Quota      int64  `db:"quota" json:"quota"` // max total tokens (0 = unlimited)
	CreatedAt  string `db:"created_at" json:"created_at"`
	LastUsedAt string `db:"last_used_at" json:"last_used_at"`
	RevokedAt  string `db:"revoked_at" json:"revoked_at"`
}

// UsageEvent is a single metered usage record (one AI call, etc.).
type UsageEvent struct {
	ID           string  `db:"id" json:"id"`
	APIKeyID     string  `db:"api_key_id" json:"api_key_id"`
	UserID       string  `db:"user_id" json:"user_id"`
	Model        string  `db:"model" json:"model"`
	InputTokens  int64   `db:"input_tokens" json:"input_tokens"`
	OutputTokens int64   `db:"output_tokens" json:"output_tokens"`
	Cost         float64 `db:"cost" json:"cost"`
	CreatedAt    string  `db:"created_at" json:"created_at"`
}

func (s *Service) migrate(ctx context.Context) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS api_keys (id text PRIMARY KEY, user_id text NOT NULL, name text NOT NULL DEFAULT '', prefix text NOT NULL, key_hash text NOT NULL, scopes text NOT NULL DEFAULT '', quota bigint NOT NULL DEFAULT 0, created_at text NOT NULL, last_used_at text NOT NULL DEFAULT '', revoked_at text NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS usage_events (id text PRIMARY KEY, api_key_id text NOT NULL DEFAULT '', user_id text NOT NULL DEFAULT '', model text NOT NULL DEFAULT '', input_tokens bigint NOT NULL DEFAULT 0, output_tokens bigint NOT NULL DEFAULT 0, cost double precision NOT NULL DEFAULT 0, created_at text NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) keys(db *sql.DB) *orm.Query[APIKey] {
	return orm.For[APIKey](db, s.k.Dialect(), "api_keys")
}
func (s *Service) usage(db *sql.DB) *orm.Query[UsageEvent] {
	return orm.For[UsageEvent](db, s.k.Dialect(), "usage_events")
}

func newID() string  { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }

// ── API keys ────────────────────────────────────────────────────────────────

// GenerateKey creates a new API key for a user and returns the plaintext ONCE.
func (s *Service) GenerateKey(ctx context.Context, userID, name, scopes string, quota int64) (APIKey, string, error) {
	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	secret := hex.EncodeToString(raw)
	prefix := "tk_" + secret[:8]
	plaintext := prefix + "_" + secret[8:]
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	db, err := s.db(ctx)
	if err != nil {
		return APIKey{}, "", err
	}
	rec, err := s.keys(db).Create(ctx, map[string]any{
		"id": newID(), "user_id": userID, "name": name, "prefix": prefix,
		"key_hash": hash, "scopes": scopes, "quota": quota, "created_at": nowRFC(),
	})
	if err != nil {
		return APIKey{}, "", err
	}
	return *rec, plaintext, nil
}

// ListKeys returns a user's API keys (without the hash).
func (s *Service) ListKeys(ctx context.Context, userID string) ([]APIKey, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	return s.keys(db).Where("user_id", "=", userID).Order("created_at DESC").Get(ctx)
}

// RevokeKey marks a key revoked.
func (s *Service) RevokeKey(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return s.keys(db).Where("id", "=", id).Update(ctx, map[string]any{"revoked_at": nowRFC()})
}

// Authenticate validates a plaintext key and returns it (touches last_used_at).
func (s *Service) Authenticate(ctx context.Context, plaintext string) (*APIKey, error) {
	sum := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(sum[:])
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := s.keys(db).Where("key_hash", "=", hash).First(ctx)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.RevokedAt != "" {
		return nil, errors.New("billing: invalid or revoked api key")
	}
	_ = s.keys(db).Where("id", "=", rec.ID).Update(ctx, map[string]any{"last_used_at": nowRFC()})
	return rec, nil
}

// Middleware authenticates a `Authorization: Bearer <key>` header and stores the
// APIKey in the request context (use KeyFromContext to read it). Requests without
// a valid key pass through unauthenticated (the handler decides what to require).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok != "" {
			if key, err := s.Authenticate(r.Context(), tok); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, key))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// KeyFromContext returns the authenticated APIKey, if any.
func KeyFromContext(ctx context.Context) (*APIKey, bool) {
	v, ok := ctx.Value(ctxKey{}).(*APIKey)
	return v, ok
}

// ── Usage metering ──────────────────────────────────────────────────────────

// RecordUsage meters one usage event (e.g. an AI call's token usage).
func (s *Service) RecordUsage(ctx context.Context, userID, apiKeyID, model string, inTok, outTok int64, cost float64) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	_, err = s.usage(db).Create(ctx, map[string]any{
		"id": newID(), "api_key_id": apiKeyID, "user_id": userID, "model": model,
		"input_tokens": inTok, "output_tokens": outTok, "cost": cost, "created_at": nowRFC(),
	})
	return err
}

// Consumption aggregates a user's usage.
type Consumption struct {
	Events       int     `json:"events"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// Summary returns a user's aggregated consumption.
func (s *Service) Summary(ctx context.Context, userID string) (Consumption, error) {
	db, err := s.db(ctx)
	if err != nil {
		return Consumption{}, err
	}
	events, err := s.usage(db).Where("user_id", "=", userID).Get(ctx)
	if err != nil {
		return Consumption{}, err
	}
	var c Consumption
	c.Events = len(events)
	for _, e := range events {
		c.InputTokens += e.InputTokens
		c.OutputTokens += e.OutputTokens
		c.Cost += e.Cost
	}
	c.TotalTokens = c.InputTokens + c.OutputTokens
	return c, nil
}

// WithinQuota reports whether a key is still under its token quota (0 = unlimited).
func (s *Service) WithinQuota(ctx context.Context, key APIKey) (bool, error) {
	if key.Quota <= 0 {
		return true, nil
	}
	c, err := s.Summary(ctx, key.UserID)
	if err != nil {
		return false, err
	}
	return c.TotalTokens < key.Quota, nil
}

// Record looks up the installed billing service from the kernel and records a
// usage event — a no-op if billing isn't installed. Other plugins (e.g. ai) call
// this to report token usage without importing the Service type.
func Record(ctx context.Context, k *togo.Kernel, userID, apiKeyID, model string, inTok, outTok int64, cost float64) {
	if v, ok := k.Get("billing"); ok {
		if s, ok := v.(*Service); ok {
			_ = s.RecordUsage(ctx, userID, apiKeyID, model, inTok, outTok, cost)
		}
	}
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// userID resolves the acting user: the authenticated API key's user, else the
// X-User-Id header or ?user_id= (auth middleware should set the key in prod).
func userID(r *http.Request) string {
	if key, ok := KeyFromContext(r.Context()); ok {
		return key.UserID
	}
	if v := r.Header.Get("X-User-Id"); v != "" {
		return v
	}
	return r.URL.Query().Get("user_id")
}

func (s *Service) routes() {
	r := s.k.Router
	base := strings.TrimRight(s.k.Config.RESTPath, "/") + "/billing"
	r.Post(base+"/keys", s.handleCreateKey)
	r.Get(base+"/keys", s.handleListKeys)
	r.Delete(base+"/keys/{id}", s.handleRevokeKey)
	r.Get(base+"/usage", s.handleUsage)
}

func (s *Service) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		Name   string `json:"name"`
		Scopes string `json:"scopes"`
		Quota  int64  `json:"quota"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	uid := body.UserID
	if uid == "" {
		uid = userID(r)
	}
	if uid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	key, plaintext, err := s.GenerateKey(r.Context(), uid, body.Name, body.Scopes, body.Quota)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": plaintext})
}

func (s *Service) handleListKeys(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	keys, err := s.ListKeys(r.Context(), uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Service) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if err := s.RevokeKey(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleUsage(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	c, err := s.Summary(r.Context(), uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, c)
}
