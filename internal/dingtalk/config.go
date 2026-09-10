package dingtalk

import (
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Config holds runtime-only transport secrets. Its zero value disables delivery,
// not the registered production capability. Never serialize the underlying URL.
type Config struct {
	webhook *url.URL
	secret  string
}

func (c Config) Enabled() bool        { return c.webhook != nil }
func (Config) String() string         { return "dingtalk.Config[redacted]" }
func (c Config) GoString() string     { return c.String() }
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

func LoadConfig(getenv func(string) string) (Config, error) {
	raw := getenv("DINGTALK_WEBHOOK_URL")
	if raw == "" {
		return Config{}, nil
	}
	invalid := errors.New("dingtalk_config_invalid")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" || u.Opaque != "" || strings.TrimSpace(raw) != raw ||
		strings.ContainsAny(raw, "\r\n\t") {
		return Config{}, invalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query["access_token"]) != 1 || strings.TrimSpace(query.Get("access_token")) == "" {
		return Config{}, invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, invalid
		}
	}
	secret := getenv("DINGTALK_SIGNING_SECRET")
	if !utf8.ValidString(secret) || strings.IndexFunc(secret, unicode.IsControl) >= 0 || strings.TrimSpace(secret) != secret {
		return Config{}, invalid
	}
	return Config{webhook: u, secret: secret}, nil
}
