package cliproxyapi

import (
	"net/url"
	"strconv"
	"strings"
)

type ConfigurationReason string

const ConfigurationInvalidEndpoint ConfigurationReason = "invalid_endpoint"

type ConfigurationError struct{ Reason ConfigurationReason }

func (e *ConfigurationError) Error() string {
	return "cliproxyapi configuration rejected: " + string(e.Reason)
}

type validatedEndpoint struct {
	scheme   string
	hostname string
	port     string
	basePath string
}

func validateEndpoint(raw string) (validatedEndpoint, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	} else {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || value == 0 {
			return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
		}
		port = strconv.FormatUint(value, 10)
	}
	basePath, ok := normalizeBasePath(parsed.EscapedPath())
	if !ok {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	return validatedEndpoint{scheme: scheme, hostname: hostname, port: port, basePath: basePath}, nil
}

func normalizeBasePath(escaped string) (string, bool) {
	if escaped == "" || escaped == "/" {
		return "", true
	}
	if !strings.HasPrefix(escaped, "/") || strings.Contains(escaped, "//") || strings.Contains(escaped, ".") {
		return "", false
	}
	return strings.TrimRight(escaped, "/"), true
}
