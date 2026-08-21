package config

import (
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Demo passwords. They are deliberately obvious: demo mode announces them on
// the login page, so they are documentation rather than secrets.
const (
	DemoPassword      = "demo"
	DemoAdminPassword = "admin"
)

// LoadRuntime reads deployment settings from the environment. The shared
// password is required; everything else has a usable default.
//
// Demo mode is the exception: it fills in throwaway passwords so someone who
// just cloned the repository can start the site with no configuration at all.
func LoadRuntime() (Runtime, error) {
	demo := envBool("DEMO", false)
	rt := Runtime{
		Demo:          demo,
		ListenAddr:    env("LISTEN_ADDR", ":8080"),
		ConfigPath:    env("CONFIG_PATH", "config.yaml"),
		DBPath:        env("DB_PATH", "data/booking.db"),
		BaseURL:       strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		Password:      os.Getenv("BOOKING_PASSWORD"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		SessionMaxAge: time.Duration(envInt("SESSION_DAYS", 30)) * 24 * time.Hour,
		TrustProxy:    envBool("TRUST_PROXY", true),
		Mattermost: MattermostSettings{
			URL:   strings.TrimRight(os.Getenv("MATTERMOST_URL"), "/"),
			Token: strings.TrimSpace(os.Getenv("MATTERMOST_TOKEN")),
			Allow: envList("MATTERMOST_ALLOW"),
		},
	}
	if demo {
		if rt.Password == "" {
			rt.Password = DemoPassword
		}
		if rt.AdminPassword == "" {
			rt.AdminPassword = DemoAdminPassword
		}
	}
	if rt.Password == "" {
		return rt, errors.New("BOOKING_PASSWORD must be set (or run with -demo to try the site out)")
	}
	// A stable secret keeps sessions alive across restarts. Deriving it from the
	// passwords means rotating a password also invalidates every session, which
	// is exactly what you want when someone moves out.
	if s := os.Getenv("SESSION_SECRET"); s != "" {
		sum := sha256.Sum256([]byte(s))
		rt.SessionSecret = sum[:]
	} else {
		sum := sha256.Sum256([]byte("rudbeckia|" + rt.Password + "|" + rt.AdminPassword))
		rt.SessionSecret = sum[:]
	}
	// Half a configuration is worse than none: it looks connected and silently
	// is not, so say so at startup instead.
	if (rt.Mattermost.URL == "") != (rt.Mattermost.Token == "") {
		return rt, errors.New("set both MATTERMOST_URL and MATTERMOST_TOKEN, or neither")
	}
	if demo {
		// The demo must never reach a real chat server, and never DM anyone.
		rt.Mattermost = MattermostSettings{}
	}
	return rt, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envList reads a comma-separated list, ignoring blanks and stray @ prefixes
// so "@anna, bo" and "anna,bo" mean the same thing.
func envList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "@")
		if part != "" {
			out = append(out, strings.ToLower(part))
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
