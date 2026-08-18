// Package config loads verdande's settings from the environment.
//
// Everything is env-driven because verdande ships as a Rune: the panel hands the
// container an environment and a /data volume, and there is nowhere to put a config
// file that the operator would ever see.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// BaseURL is the address verdande answers on from outside. Invite links,
	// password resets, ICS feeds and OAuth redirects are all built from it, so it
	// has to be the public address, not the listen address.
	BaseURL string
	Addr    string
	DataDir string

	SessionTTL time.Duration
	InviteTTL  time.Duration
	ResetTTL   time.Duration

	SMTP SMTP

	// Gmail is the OAuth client the operator registered in Google Cloud. It
	// belongs to the instance: one registration, any number of users.
	GmailClientID     string
	GmailClientSecret string

	// UpdateCheck asks GitHub whether a newer release exists. Off unless asked
	// for: a self-hosted app that reaches out without being told to has broken
	// the deal its operator made by self-hosting.
	UpdateCheck bool

	// The Yggdrasil panel this instance runs under, so it can ask to be restarted
	// from its own interface rather than from another browser tab.
	//
	// A container cannot replace its own image from the inside. Restarting is the
	// panel's job — it recreates the container and pulls `:latest` on the way — so
	// what this needs is a way to ask. All three are required together; any one
	// alone cannot do anything.
	//
	// The token is a panel credential with control over that server. Anybody who
	// can read this environment can restart it, which is true of anybody with the
	// host anyway. It is never sent to a browser: the settings page is told only
	// whether it is set.
	// GmailSyncBudget bounds one "fetch now", well under whatever proxy sits in
	// front of this server. Configurable so a test can prove the budget is applied
	// without spending the real one on every run.
	GmailSyncBudget time.Duration

	// GmailEndpoints redirects the Gmail calls at a server the tests control. Empty
	// in production, where it means Google.
	GmailTokenURL string
	GmailAPIURL   string

	PanelURL      string
	PanelToken    string
	PanelServerID string

	// TrashRetention is how long a soft-deleted task or project stays recoverable.
	TrashRetention time.Duration

	Dev bool
}

type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// StartTLS is the common case for a self-hosted Mailcow on 587. Port 465 is
	// implicit TLS instead and sets this false.
	StartTLS bool
	Insecure bool
}

// Configured reports whether outbound mail can be sent. Without a host verdande
// still runs — it just surfaces invite links in the UI instead of mailing them.
func (s SMTP) Configured() bool { return s.Host != "" }

func Load() (*Config, error) {
	c := &Config{
		BaseURL:        env("VERDANDE_BASE_URL", "http://localhost:8080"),
		Addr:           env("VERDANDE_ADDR", ":8080"),
		DataDir:        env("VERDANDE_DATA_DIR", "/data"),
		TrashRetention: 30 * 24 * time.Hour,
		Dev:            envBool("VERDANDE_DEV", false),

		GmailClientID:     env("VERDANDE_GMAIL_CLIENT_ID", ""),
		GmailSyncBudget:   25 * time.Second,
		PanelURL:          strings.TrimSuffix(env("VERDANDE_PANEL_URL", ""), "/"),
		PanelToken:        env("VERDANDE_PANEL_TOKEN", ""),
		PanelServerID:     env("VERDANDE_PANEL_SERVER_ID", ""),
		GmailClientSecret: env("VERDANDE_GMAIL_CLIENT_SECRET", ""),
		UpdateCheck:       envBool("VERDANDE_UPDATE_CHECK", false),
	}

	var err error
	if c.SessionTTL, err = envDuration("VERDANDE_SESSION_TTL", 30*24*time.Hour); err != nil {
		return nil, err
	}
	if c.InviteTTL, err = envDuration("VERDANDE_INVITE_TTL", 7*24*time.Hour); err != nil {
		return nil, err
	}
	if c.ResetTTL, err = envDuration("VERDANDE_RESET_TTL", time.Hour); err != nil {
		return nil, err
	}

	port, err := envInt("VERDANDE_SMTP_PORT", 587)
	if err != nil {
		return nil, err
	}
	c.SMTP = SMTP{
		Host:     env("VERDANDE_SMTP_HOST", ""),
		Port:     port,
		Username: env("VERDANDE_SMTP_USER", ""),
		Password: env("VERDANDE_SMTP_PASS", ""),
		From:     env("VERDANDE_SMTP_FROM", "verdande@localhost"),
		// Port 465 is implicit TLS from the first byte; everything else negotiates
		// with STARTTLS. An operator can still override the guess.
		StartTLS: envBool("VERDANDE_SMTP_STARTTLS", port != 465),
		Insecure: envBool("VERDANDE_SMTP_INSECURE", false),
	}

	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return nil, fmt.Errorf("VERDANDE_DATA_DIR: %w", err)
	}
	return c, nil
}

// DBPath, FilesDir and BackupsDir are the three things that live on the /data volume.
func (c *Config) DBPath() string     { return filepath.Join(c.DataDir, "verdande.db") }
func (c *Config) FilesDir() string   { return filepath.Join(c.DataDir, "files") }
func (c *Config) BackupsDir() string { return filepath.Join(c.DataDir, "backups") }

// GmailRedirectURL has to match what was registered in Google Cloud exactly,
// including the scheme — which is why it is derived from BaseURL rather than
// configured separately and left to drift out of step with it.
func (c *Config) GmailRedirectURL() string { return c.BaseURL + "/oauth/gmail/callback" }

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", key, v)
	}
	return n, nil
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (try 720h)", key, v)
	}
	return d, nil
}
