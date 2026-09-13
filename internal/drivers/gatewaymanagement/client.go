// Package gatewaymanagement contains the closed HTTP-only management probe
// used for Control-managed Gateway observations.
package gatewaymanagement

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	HealthPath          = "/health"
	DefaultProbeTimeout = 5 * time.Second
)

var errRedirectRejected = errors.New("gatewaymanagement: redirect rejected")

type OriginErrorReason string

const (
	OriginInvalid      OriginErrorReason = "invalid_origin"
	OriginSchemeReject OriginErrorReason = "scheme_rejected"
	OriginHTTPSReject  OriginErrorReason = "https_rejected"
)

// OriginError is a sanitized management-origin validation error. It does not
// expose the rejected target or any parser detail.
type OriginError struct {
	Reason OriginErrorReason
}

func (e *OriginError) Error() string {
	if e == nil {
		return "gatewaymanagement: invalid origin"
	}
	return "gatewaymanagement: " + string(e.Reason)
}

// Origin is an immutable, normalized HTTP management origin.
type Origin struct {
	url url.URL
}

// ValidateOrigin accepts only an absolute HTTP origin without credentials,
// path, query, or fragment. HTTPS and every other scheme are rejected before
// a client can issue a request.
func ValidateOrigin(raw string) (Origin, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return Origin{}, &OriginError{Reason: OriginInvalid}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return Origin{}, &OriginError{Reason: OriginInvalid}
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return Origin{}, &OriginError{Reason: OriginHTTPSReject}
	}
	if !strings.EqualFold(parsed.Scheme, "http") {
		return Origin{}, &OriginError{Reason: OriginSchemeReject}
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return Origin{}, &OriginError{Reason: OriginInvalid}
	}
	if strings.Contains(parsed.Hostname(), "..") {
		return Origin{}, &OriginError{Reason: OriginInvalid}
	}

	normalized := *parsed
	normalized.Scheme = "http"
	hostname := strings.ToLower(parsed.Hostname())
	if port := parsed.Port(); port != "" && port != "80" {
		normalized.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		normalized.Host = "[" + hostname + "]"
	} else {
		normalized.Host = hostname
	}
	normalized.Path = ""
	normalized.RawPath = ""
	normalized.RawQuery = ""
	normalized.Fragment = ""
	return Origin{url: normalized}, nil
}

func (o Origin) requestURL(path string) string {
	target := o.url
	target.Path = path
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	return target.String()
}

// URL returns a request URL rooted at the validated origin. Callers supply
// only a fixed path from their closed management contract.
func (o Origin) URL(path string) string { return o.requestURL(path) }

type ProbeReason string

const (
	ProbeReasonNone               ProbeReason = "none"
	ProbeReasonContextRejected    ProbeReason = "context_rejected"
	ProbeReasonTimeout            ProbeReason = "timeout"
	ProbeReasonCancelled          ProbeReason = "cancelled"
	ProbeReasonNetworkUnavailable ProbeReason = "network_unavailable"
	ProbeReasonRedirectRejected   ProbeReason = "redirect_rejected"
	ProbeReasonHTTPStatus         ProbeReason = "http_status"
)

// ProbeObservation contains only bounded product-level health information.
// Response bodies and headers are intentionally not exposed or retained.
type ProbeObservation struct {
	Healthy    bool
	HTTPStatus int
	Reason     ProbeReason
}

// ProbeError is a sanitized probe failure. HTTPStatus is set only when a
// response with a bounded status code was received.
type ProbeError struct {
	Reason     ProbeReason
	HTTPStatus int
}

func (e *ProbeError) Error() string {
	if e == nil {
		return "gatewaymanagement: probe failed"
	}
	return "gatewaymanagement: probe failed: " + string(e.Reason)
}

// Client performs one fixed read-only GET /health against one validated
// management origin. The caller supplies the timeout through ctx; callers
// should use DefaultProbeTimeout for the Phase 6 five-second convention.
type Client struct {
	origin     Origin
	httpClient *http.Client
}

func NewClient(managementOrigin string) (*Client, error) {
	origin, err := ValidateOrigin(managementOrigin)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:              nil,
		ForceAttemptHTTP2:  false,
		DisableKeepAlives:  true,
		DisableCompression: true,
		MaxConnsPerHost:    1,
	}
	return &Client{
		origin: origin,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errRedirectRejected
			},
		},
	}, nil
}

func (c *Client) Probe(ctx context.Context) (ProbeObservation, error) {
	if c == nil || c.httpClient == nil {
		return ProbeObservation{Reason: ProbeReasonContextRejected}, &ProbeError{Reason: ProbeReasonContextRejected}
	}
	if ctx == nil {
		return ProbeObservation{Reason: ProbeReasonContextRejected}, &ProbeError{Reason: ProbeReasonContextRejected}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin.requestURL(HealthPath), nil)
	if err != nil {
		return ProbeObservation{Reason: ProbeReasonContextRejected}, &ProbeError{Reason: ProbeReasonContextRejected}
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return classifyProbeError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		observation := ProbeObservation{HTTPStatus: response.StatusCode, Reason: ProbeReasonHTTPStatus}
		return observation, &ProbeError{Reason: ProbeReasonHTTPStatus, HTTPStatus: response.StatusCode}
	}
	return ProbeObservation{Healthy: true, HTTPStatus: http.StatusOK, Reason: ProbeReasonNone}, nil
}

func classifyProbeError(ctx context.Context, err error) (ProbeObservation, error) {
	switch {
	case errors.Is(err, errRedirectRejected):
		return ProbeObservation{Reason: ProbeReasonRedirectRejected}, &ProbeError{Reason: ProbeReasonRedirectRejected}
	case errors.Is(err, context.Canceled):
		return ProbeObservation{Reason: ProbeReasonCancelled}, &ProbeError{Reason: ProbeReasonCancelled}
	case errors.Is(err, context.DeadlineExceeded):
		return ProbeObservation{Reason: ProbeReasonTimeout}, &ProbeError{Reason: ProbeReasonTimeout}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ProbeObservation{Reason: ProbeReasonTimeout}, &ProbeError{Reason: ProbeReasonTimeout}
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ProbeObservation{Reason: ProbeReasonTimeout}, &ProbeError{Reason: ProbeReasonTimeout}
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return ProbeObservation{Reason: ProbeReasonCancelled}, &ProbeError{Reason: ProbeReasonCancelled}
	}
	return ProbeObservation{Reason: ProbeReasonNetworkUnavailable}, &ProbeError{Reason: ProbeReasonNetworkUnavailable}
}
