package gatewaydirectory

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	DefaultBodyLimitBytes int64 = 4 << 20

	directoryPath = "/internal/v1/api-account-directory"
)

var errRedirectRejected = errors.New("gatewaydirectory: redirect rejected")

// FetchError keeps the failure classification closed without exposing raw
// network details, response bodies, or endpoint data.
type FetchError struct {
	Reason     rootdrivers.Reason
	HTTPStatus int
	Retryable  bool
	Err        error
}

func (e *FetchError) Error() string {
	if e == nil {
		return "gatewaydirectory: fetch failed"
	}
	return "gatewaydirectory: fetch failed: " + string(e.Reason)
}

func (e *FetchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Client performs exactly one fixed read-only Directory request against a
// validated management origin.
type Client struct {
	baseURL        *url.URL
	httpClient     *http.Client
	secretResolver rootdrivers.SecretResolver
}

func NewClient(managementOrigin string, secretResolver rootdrivers.SecretResolver) (*Client, error) {
	baseURL, err := validateManagementOrigin(managementOrigin)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:              nil,
		ForceAttemptHTTP2:  false,
		DisableKeepAlives:  true,
		DisableCompression: true,
		MaxConnsPerHost:    1,
		TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, // Management outbound contract: deployment owns target trust.
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errRedirectRejected
			},
		},
		secretResolver: secretResolver,
	}, nil
}

func (c *Client) Fetch(ctx context.Context, reference rootdrivers.SecretReference) (DirectoryResponse, [sha256.Size]byte, error) {
	if c == nil || c.baseURL == nil || c.httpClient == nil || c.secretResolver == nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, &FetchError{
			Reason:    rootdrivers.ReasonSecretUnavailable,
			Retryable: false,
		}
	}
	secret, err := c.secretResolver.Resolve(ctx, reference)
	if err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, classifySecretResolveError(err)
	}
	defer secret.Destroy()

	var response DirectoryResponse
	var fingerprint [sha256.Size]byte
	err = secret.Use(func(token string) error {
		requestURL := *c.baseURL
		requestURL.Path = directoryPath
		requestURL.RawQuery = ""
		requestURL.Fragment = ""

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return &FetchError{Reason: rootdrivers.ReasonResponseInvalid, Retryable: false, Err: err}
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)

		httpResponse, err := c.httpClient.Do(request)
		if err != nil {
			return classifyRequestError(ctx, err)
		}
		defer httpResponse.Body.Close()

		switch {
		case httpResponse.StatusCode == http.StatusOK:
			parsed, fingerprintValue, parseErr := parseDirectoryResponse(httpResponse.Body, DefaultBodyLimitBytes)
			if parseErr != nil {
				if ctx.Err() != nil {
					return classifyRequestError(ctx, ctx.Err())
				}
				return parseErr
			}
			response = parsed
			fingerprint = fingerprintValue
			return nil
		case httpResponse.StatusCode == http.StatusTooManyRequests || httpResponse.StatusCode >= http.StatusInternalServerError:
			return &FetchError{
				Reason:     rootdrivers.ReasonHTTPStatus,
				HTTPStatus: httpResponse.StatusCode,
				Retryable:  true,
				Err:        fmt.Errorf("gatewaydirectory: unexpected http status %d", httpResponse.StatusCode),
			}
		default:
			return &FetchError{
				Reason:     rootdrivers.ReasonHTTPStatus,
				HTTPStatus: httpResponse.StatusCode,
				Retryable:  false,
				Err:        fmt.Errorf("gatewaydirectory: unexpected http status %d", httpResponse.StatusCode),
			}
		}
	})
	if err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, err
	}
	return response, fingerprint, nil
}

func validateManagementOrigin(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, &FetchError{Reason: rootdrivers.ReasonTargetRejected, Retryable: false}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &FetchError{Reason: rootdrivers.ReasonTargetRejected, Retryable: false}
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "https" && scheme != "http" {
		return nil, &FetchError{Reason: rootdrivers.ReasonTLSRejected, Retryable: false}
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, &FetchError{Reason: rootdrivers.ReasonTargetRejected, Retryable: false}
	}
	if strings.Contains(parsed.Hostname(), "..") {
		return nil, &FetchError{Reason: rootdrivers.ReasonTargetRejected, Retryable: false}
	}
	normalized := *parsed
	normalized.Scheme = strings.ToLower(parsed.Scheme)
	normalized.Path = ""
	normalized.RawPath = ""
	return &normalized, nil
}

func classifySecretResolveError(err error) error {
	switch {
	case errors.Is(err, rootdrivers.ErrSecretReferenceUnknown):
		return &FetchError{Reason: rootdrivers.ReasonSecretReferenceUnknown, Retryable: false, Err: err}
	case errors.Is(err, rootdrivers.ErrSecretProviderUnknown):
		return &FetchError{Reason: rootdrivers.ReasonSecretProviderUnknown, Retryable: false, Err: err}
	case errors.Is(err, rootdrivers.ErrSecretFileUnsafe):
		return &FetchError{Reason: rootdrivers.ReasonSecretFileUnsafe, Retryable: false, Err: err}
	default:
		return &FetchError{Reason: rootdrivers.ReasonSecretUnavailable, Retryable: false, Err: err}
	}
}

func classifyRequestError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errRedirectRejected):
		return &FetchError{Reason: rootdrivers.ReasonRedirectRejected, Retryable: false, Err: err}
	case errors.Is(err, context.Canceled):
		return &FetchError{Reason: rootdrivers.ReasonCancelled, Retryable: false, Err: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &FetchError{Reason: rootdrivers.ReasonTimeout, Retryable: true, Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &FetchError{Reason: rootdrivers.ReasonTimeout, Retryable: true, Err: err}
		}
		return &FetchError{Reason: rootdrivers.ReasonNetworkUnavailable, Retryable: true, Err: err}
	}
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &FetchError{Reason: rootdrivers.ReasonTimeout, Retryable: true, Err: err}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return &FetchError{Reason: rootdrivers.ReasonCancelled, Retryable: false, Err: err}
		}
	}
	return &FetchError{Reason: rootdrivers.ReasonNetworkUnavailable, Retryable: true, Err: err}
}
