package auth

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
)

const minBootstrapSecretBytes = 32

type Config struct {
	Environment         Environment
	BindAddress         string
	BootstrapSecretFile string
	AuthKeyringFile     string
	TrustedProxyCIDRs   []string
	CookieSecure        bool
	MFARequired         bool
}

type ValidatedConfig struct {
	Config
	TrustedProxies []netip.Prefix
	Keyring        *Keyring
}

func (c Config) Validate() (*ValidatedConfig, error) {
	if err := requireEnum("environment", c.Environment, c.Environment.Valid()); err != nil {
		return nil, err
	}
	if c.BindAddress == "" {
		return nil, errors.New("auth: bind address is required")
	}

	if c.Environment != EnvironmentDev {
		if !c.CookieSecure {
			return nil, errors.New("auth: secure cookies are required outside dev")
		}
		if c.AuthKeyringFile == "" {
			return nil, errors.New("auth: keyring file is required outside dev")
		}
	} else if !c.CookieSecure && !isLoopbackBind(c.BindAddress) {
		return nil, errors.New("auth: insecure dev cookies require a loopback bind address")
	}
	if c.Environment == EnvironmentProduction && !c.MFARequired {
		return nil, errors.New("auth: MFA is required in production")
	}

	prefixes, err := parsePrefixes(c.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}

	validated := &ValidatedConfig{Config: c, TrustedProxies: prefixes}
	if c.AuthKeyringFile != "" {
		keyring, err := LoadKeyringFile(c.AuthKeyringFile, c.Environment)
		if err != nil {
			return nil, fmt.Errorf("auth: load keyring: %w", err)
		}
		validated.Keyring = keyring
	}
	if c.BootstrapSecretFile != "" {
		if err := validateSecretFile(c.BootstrapSecretFile, minBootstrapSecretBytes); err != nil {
			return nil, fmt.Errorf("auth: bootstrap secret file: %w", err)
		}
	}
	return validated, nil
}

func parsePrefixes(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, errors.New("auth: invalid trusted proxy CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func isLoopbackBind(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func validateSecretFile(path string, minBytes int) error {
	info, err := os.Lstat(filepath.Clean(path))
	if err != nil {
		return errors.New("secret file is unavailable")
	}
	if !info.Mode().IsRegular() {
		return errors.New("secret path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("secret file permissions allow group or other access")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return errors.New("secret file is unreadable")
	}
	if len(data) < minBytes {
		return errors.New("secret file is too short")
	}
	return nil
}
