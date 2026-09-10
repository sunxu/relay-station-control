package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

const HTTPTimeout = 5 * time.Second
const responseLimit = 8 * 1024

// Executor has no domain repository: the immutable payload is the entire display
// snapshot. The HTTP client neither inherits proxy configuration nor redirects.
type Executor struct {
	config Config
	client *http.Client
}

func NewExecutor(config Config) *Executor {
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: HTTPTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: HTTPTimeout,
		DisableCompression:  true,
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Executor{config: config, client: &http.Client{
		Transport: transport, Timeout: HTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (e *Executor) Execute(ctx context.Context, execution jobs.Execution) jobs.ExecuteResult {
	if !e.config.Enabled() {
		return result(jobs.ExecutePermanentFailure, "dingtalk_disabled")
	}
	body, err := renderPayload(execution.Payload)
	if err != nil {
		return result(jobs.ExecutePermanentFailure, "dingtalk_invalid_payload")
	}
	u := *e.config.webhook
	if e.config.secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		query := u.Query()
		query.Set("timestamp", timestamp)
		query.Set("sign", signature(timestamp, e.config.secret))
		u.RawQuery = query.Encode()
	}
	// GotConn occurs after DNS/connect/TLS completion, before a request can be
	// written. Once reached, any transport failure is conservatively ambiguous.
	// Never infer no-effect merely from a failed WroteRequest callback.
	var gotConnection atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { gotConnection.Store(true) },
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return result(jobs.ExecutePermanentFailure, "dingtalk_config_invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := e.client.Do(req)
	if err != nil {
		// Client errors include the secret URL; only fixed framework-safe codes
		// leave this method. There is deliberately no raw-error logging.
		if !gotConnection.Load() {
			return result(jobs.ExecuteRetryableNoEffect, "dingtalk_connect_failed")
		}
		return result(jobs.ExecuteResultUnknown, "dingtalk_result_unknown")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 && response.StatusCode <= 599 {
		// An HTTP status alone does not prove the operation was never applied.
		return result(jobs.ExecuteResultUnknown, "dingtalk_http_retry")
	}
	if response.StatusCode != http.StatusOK {
		return result(jobs.ExecutePermanentFailure, "dingtalk_http_rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return result(jobs.ExecuteResultUnknown, "dingtalk_result_unknown")
	}
	if len(raw) > responseLimit {
		return result(jobs.ExecutePermanentFailure, "dingtalk_invalid_response")
	}
	return classifyBusinessResponse(raw)
}

// Current official security settings: UTF-8 HMAC-SHA256(secret,
// timestampMillis + "\n" + secret), Base64, then query URL encoding by Values.Encode.
// https://open.dingtalk.com/document/robots/customize-robot-security-settings
func signature(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func classifyBusinessResponse(raw []byte) jobs.ExecuteResult {
	invalid := result(jobs.ExecutePermanentFailure, "dingtalk_invalid_response")
	// Reject duplicate keys and trailing documents; neither may turn an error
	// into success by a parser's last-key-wins behavior.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return invalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return invalid
		}
		name, ok := key.(string)
		if !ok {
			return invalid
		}
		if _, exists := fields[name]; exists {
			return invalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return invalid
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return invalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalid
	}
	var message string
	if json.Unmarshal(fields["errmsg"], &message) != nil || message == "" {
		return invalid
	}
	codeText := string(fields["errcode"])
	// The current official field table says Number, while its return example
	// uses "0". Accept those two documented scalar encodings, not null/bool/float.
	if len(codeText) > 0 && codeText[0] == '"' {
		if json.Unmarshal(fields["errcode"], &codeText) != nil {
			return invalid
		}
	}
	code, err := strconv.ParseInt(codeText, 10, 32)
	if err != nil || strconv.FormatInt(code, 10) != codeText {
		return invalid
	}
	switch code {
	case 0:
		if message != "ok" {
			return invalid
		}
		return result(jobs.ExecuteSucceeded, "")
	case 410100:
		// Official interface error table: sending rate exceeded; request rejected.
		return result(jobs.ExecuteRetryableNoEffect, "dingtalk_business_retry")
	case -1:
		// System busy: retry is documented, but absence of effect is not.
		return result(jobs.ExecuteResultUnknown, "dingtalk_business_retry")
	default:
		return result(jobs.ExecutePermanentFailure, "dingtalk_business_rejected")
	}
}

func result(disposition jobs.ExecuteDisposition, code string) jobs.ExecuteResult {
	return jobs.ExecuteResult{Disposition: disposition, ErrorCode: code}
}

// These fail-closed interface methods perform no I/O. Normal delivery and lease
// recovery use the two persisted policies, never a receipt/rollback protocol.
func (*Executor) Verify(context.Context, jobs.Execution) jobs.VerifyResult {
	return jobs.VerifyResult{Disposition: jobs.VerifyEffectUnknown, ErrorCode: "dingtalk_unsupported_operation"}
}
func (*Executor) Rollback(context.Context, jobs.Execution) jobs.RollbackResult {
	return jobs.RollbackResult{Disposition: jobs.RollbackFailed, ErrorCode: "dingtalk_unsupported_operation"}
}
