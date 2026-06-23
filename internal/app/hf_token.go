package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zboya/nurvis/internal/store/repo"
)

// HuggingFaceCredProvider is the provider key under which HF tokens are stored
// in the site_credentials table.
const HuggingFaceCredProvider = "huggingface"

// huggingfaceTokenFromCreds returns a modelmgr.TokenProviderFunc backed by the
// site_credentials table. The lookup result is cached briefly so high-frequency
// download progress polling doesn't hammer the DB; the TTL is short enough that
// users editing the token in the UI see the change within seconds.
func huggingfaceTokenFromCreds(creds *repo.SiteCredentialRepo) func(ctx context.Context) string {
	var (
		mu        sync.Mutex
		cached    string
		cachedAt  time.Time
		cacheTTL  = 5 * time.Second
		emptyMark = "\x00"
	)

	return func(ctx context.Context) string {
		mu.Lock()
		if time.Since(cachedAt) < cacheTTL && cached != "" {
			v := cached
			mu.Unlock()
			if v == emptyMark {
				return ""
			}
			return v
		}
		mu.Unlock()

		token := lookupHFToken(ctx, creds)

		mu.Lock()
		if token == "" {
			cached = emptyMark
		} else {
			cached = token
		}
		cachedAt = time.Now()
		mu.Unlock()

		return token
	}
}

// lookupHFToken pulls the most recently updated enabled HF credential and
// extracts the api_token field. Any JSON parse / DB error is logged and
// treated as "no token configured" so callers fall back to anonymous access.
func lookupHFToken(ctx context.Context, creds *repo.SiteCredentialRepo) string {
	if creds == nil {
		return ""
	}
	c, err := creds.LookupByProvider(ctx, HuggingFaceCredProvider, "")
	if err != nil {
		slog.Warn("hf token: lookup failed", "err", err)
		return ""
	}
	if c == nil {
		return ""
	}
	var cfg struct {
		APIToken string `json:"api_token"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal([]byte(c.ConfigJSON), &cfg); err != nil {
		slog.Warn("hf token: parse config_json failed", "id", c.ID, "err", err)
		return ""
	}
	tok := strings.TrimSpace(cfg.APIToken)
	if tok == "" {
		tok = strings.TrimSpace(cfg.Token)
	}
	return tok
}
