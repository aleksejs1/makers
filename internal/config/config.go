// Package config loads runtime configuration from environment variables
// and command-line flags. Flags take precedence over the environment.
package config

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime settings.
type Config struct {
	Env         string // "prod" or "dev"
	Port        int
	BaseURL     string
	DBPath      string
	Secret      []byte
	AdminEmails []string
	// TrustedProxies lists reverse proxies whose X-Forwarded-For is trusted.
	TrustedProxies []netip.Prefix

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      string // starttls, tls or none

	// SecretGenerated is true when no secret was configured and a random
	// one was generated for this process.
	SecretGenerated bool
}

// IsDev reports whether the app runs in development mode.
func (c *Config) IsDev() bool { return c.Env == "dev" }

// SecureCookies reports whether cookies should carry the Secure flag.
func (c *Config) SecureCookies() bool { return strings.HasPrefix(c.BaseURL, "https://") }

// IsAdmin reports whether the given (normalized) email belongs to an admin.
func (c *Config) IsAdmin(email string) bool {
	for _, e := range c.AdminEmails {
		if e == email {
			return true
		}
	}
	return false
}

// Load reads configuration from the environment and the given arguments.
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	c := &Config{}
	var secret, admins, proxies string
	fs.StringVar(&c.Env, "env", env("MAKERS_ENV", "prod"), "environment: prod or dev")
	fs.IntVar(&c.Port, "port", envInt("MAKERS_PORT", 8080), "HTTP port")
	fs.StringVar(&c.BaseURL, "base-url", env("MAKERS_BASE_URL", ""), "public base URL, e.g. https://makers.lv")
	fs.StringVar(&c.DBPath, "db", env("MAKERS_DB_PATH", "makers.db"), "path to the SQLite database file")
	fs.StringVar(&secret, "secret", env("MAKERS_SECRET", ""), "secret key for hashing one-time codes")
	fs.StringVar(&admins, "admin-emails", env("MAKERS_ADMIN_EMAILS", ""), "comma-separated admin emails")
	fs.StringVar(&proxies, "trusted-proxies", env("MAKERS_TRUSTED_PROXIES", ""), "comma-separated IPs/CIDRs of reverse proxies whose X-Forwarded-For is trusted")
	fs.StringVar(&c.SMTPHost, "smtp-host", env("SMTP_HOST", ""), "SMTP host (empty: log emails instead of sending)")
	fs.IntVar(&c.SMTPPort, "smtp-port", envInt("SMTP_PORT", 587), "SMTP port (465 for implicit TLS)")
	fs.StringVar(&c.SMTPUser, "smtp-user", env("SMTP_USER", ""), "SMTP username")
	fs.StringVar(&c.SMTPPassword, "smtp-password", env("SMTP_PASSWORD", ""), "SMTP password")
	fs.StringVar(&c.SMTPTLS, "smtp-tls", env("SMTP_TLS", ""), "SMTP encryption: starttls, tls or none (default: tls for port 465, else starttls)")
	fs.StringVar(&c.SMTPFrom, "smtp-from", env("SMTP_FROM", "makers.lv <noreply@makers.lv>"), "From address for emails")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if c.Env != "prod" && c.Env != "dev" {
		return nil, fmt.Errorf("invalid env %q: must be prod or dev", c.Env)
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d", c.Port)
	}
	if c.BaseURL == "" {
		c.BaseURL = fmt.Sprintf("http://localhost:%d", c.Port)
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("base URL must be an absolute http(s) URL")
	}

	for _, p := range strings.Split(proxies, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if a, err := netip.ParseAddr(p); err == nil {
				p += "/" + strconv.Itoa(a.BitLen())
			}
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", p, err)
		}
		c.TrustedProxies = append(c.TrustedProxies, prefix.Masked())
	}

	// Without SMTP, login codes are written to the log. That is fine for
	// development but would let anyone with log access log in as anyone.
	if c.SMTPHost == "" && !c.IsDev() {
		return nil, errors.New("SMTP_HOST is required in production (use MAKERS_ENV=dev to log emails instead)")
	}

	switch c.SMTPTLS {
	case "":
		c.SMTPTLS = "starttls"
		if c.SMTPPort == 465 {
			c.SMTPTLS = "tls"
		}
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("invalid SMTP TLS mode %q: must be starttls, tls or none", c.SMTPTLS)
	}

	if secret == "" {
		c.Secret = make([]byte, 32)
		_, _ = rand.Read(c.Secret)
		c.SecretGenerated = true
	} else {
		if len(secret) < 16 {
			return nil, errors.New("secret must be at least 16 characters")
		}
		c.Secret = []byte(secret)
	}

	for _, e := range strings.Split(admins, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			c.AdminEmails = append(c.AdminEmails, e)
		}
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
