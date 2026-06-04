package config

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings, sourced from environment variables.
type Config struct {
	DBPath string

	TGToken   string
	TGAdminID int64

	// Optional proxy for the Telegram connection (http/https/socks5).
	TGProxyURL  string
	TGProxyUser string
	TGProxyPass string

	VKToken   string
	// Optional user token used ONLY for polls.getVoters (read-only), to enable
	// cross-platform vote dedup. polls.getVoters is unavailable with group auth.
	// Empty = additive (non-deduplicated) tally.
	VKUserToken string
	VKGroupID   int // community id; 0 = auto-detect via groups.getById
	VKPeerID    int // chat peer to watch (e.g. 2000000001); 0 = all peers the bot sees
	MentionTags []string // case-insensitive substrings that trigger an @all broadcast

	PollRefresh time.Duration // how often active polls are re-synced from VK
	LinkCodeTTL time.Duration // lifetime of a one-time link code
}

// Load reads configuration from the environment and validates required fields.
func Load() (*Config, error) {
	c := &Config{
		DBPath:      env("BOT_DB_PATH", "./data/bot.db"),
		TGToken:     os.Getenv("TG_BOT_TOKEN"),
		TGProxyURL:  strings.TrimSpace(os.Getenv("TG_PROXY_URL")),
		TGProxyUser: os.Getenv("TG_PROXY_USER"),
		TGProxyPass: os.Getenv("TG_PROXY_PASS"),
		VKToken:     os.Getenv("VK_TOKEN"),
		VKUserToken: strings.TrimSpace(os.Getenv("VK_USER_TOKEN")),
		PollRefresh: envDuration("POLL_REFRESH_SEC", 30*time.Second),
		LinkCodeTTL: envDuration("LINK_CODE_TTL_MIN", 30*time.Minute),
	}

	var errs []string
	if c.TGToken == "" {
		errs = append(errs, "TG_BOT_TOKEN is required")
	}
	if c.VKToken == "" {
		errs = append(errs, "VK_TOKEN is required")
	}

	adminID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("TG_ADMIN_ID")), 10, 64)
	if err != nil || adminID == 0 {
		errs = append(errs, "TG_ADMIN_ID must be a non-zero integer")
	}
	c.TGAdminID = adminID

	c.VKGroupID = envInt("VK_GROUP_ID", 0)
	c.VKPeerID = envInt("VK_PEER_ID", 0)

	tags := env("MENTION_TAGS", "@all,@все,@online")
	for _, t := range strings.Split(tags, ",") {
		if t = strings.TrimSpace(strings.ToLower(t)); t != "" {
			c.MentionTags = append(c.MentionTags, t)
		}
	}

	if len(errs) > 0 {
		return nil, errors.New("config: " + strings.Join(errs, "; "))
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// envDuration reads an integer number of seconds (for *_SEC) or minutes (for *_MIN).
func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if strings.HasSuffix(key, "_MIN") {
		return time.Duration(n) * time.Minute
	}
	return time.Duration(n) * time.Second
}

// TGHTTPClient builds an *http.Client routed through the configured proxy for
// the Telegram connection. Returns (nil, nil) when no proxy is configured, in
// which case the caller should use a direct connection.
//
// Credentials may be embedded in TG_PROXY_URL (http://user:pass@host:port) or
// supplied separately via TG_PROXY_USER / TG_PROXY_PASS.
func (c *Config) TGHTTPClient() (*http.Client, error) {
	if c.TGProxyURL == "" {
		return nil, nil
	}
	u, err := url.Parse(c.TGProxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse TG_PROXY_URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("TG_PROXY_URL must be like scheme://host:port, got %q", c.TGProxyURL)
	}
	if u.User == nil && c.TGProxyUser != "" {
		if c.TGProxyPass != "" {
			u.User = url.UserPassword(c.TGProxyUser, c.TGProxyPass)
		} else {
			u.User = url.User(c.TGProxyUser)
		}
	}
	// No client Timeout: Telegram getUpdates uses long polling (~30s held open).
	transport := &http.Transport{Proxy: http.ProxyURL(u)}
	return &http.Client{Transport: transport}, nil
}

// TGProxyRedacted returns the proxy URL with any password masked, safe for logs.
func (c *Config) TGProxyRedacted() string {
	if c.TGProxyURL == "" {
		return ""
	}
	if u, err := url.Parse(c.TGProxyURL); err == nil {
		return u.Redacted()
	}
	return "(set)"
}

// String renders a redacted summary for logging.
func (c *Config) String() string {
	proxy := "off"
	if c.TGProxyURL != "" {
		proxy = "on"
	}
	dedup := "additive"
	if c.VKUserToken != "" {
		dedup = "user-token"
	}
	return fmt.Sprintf("db=%s admin=%d vk_group=%d vk_peer=%d tags=%v refresh=%s tg_proxy=%s dedup=%s",
		c.DBPath, c.TGAdminID, c.VKGroupID, c.VKPeerID, c.MentionTags, c.PollRefresh, proxy, dedup)
}
