package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	FrozenRuntimeVersion          = "7.3.2"
	FrozenRuntimeCommit           = "0b34a22fcaec392d39f710f3a8418595b491607d"
	nativeMutationBodyLimit int64 = 64 << 10
	nativeUploadPrefix            = "antigravity-"
	nativeUploadSuffix            = ".json"
	maxCreateEmailBytes           = 238
)

var safeNativeBasename = regexp.MustCompile(`^[^/\\\x00]+$`)

type NativeFailureCode string

const (
	NativeFailureUnsupportedNodeVersion    NativeFailureCode = "unsupported_node_version"
	NativeFailureNodeManagementUnavailable NativeFailureCode = "node_management_unavailable"
	NativeFailureTargetNotFound            NativeFailureCode = "account_target_not_found"
	NativeFailureTargetAmbiguous           NativeFailureCode = "account_target_ambiguous"
	NativeFailureTargetExists              NativeFailureCode = "account_target_exists"
	NativeFailureFilenameConflict          NativeFailureCode = "account_filename_conflict"
	NativeFailureInvalidRequest            NativeFailureCode = "invalid_request"
)

type NativeOutcomeKind string

const (
	NativeOutcomeApplied NativeOutcomeKind = "remote_applied"
	NativeOutcomeNoop    NativeOutcomeKind = "remote_noop"
	NativeOutcomeUnknown NativeOutcomeKind = "outcome_unknown"
	NativeOutcomeFailed  NativeOutcomeKind = "failed"
)

type NativeMutationOutcome struct {
	Kind        NativeOutcomeKind
	FailureCode NativeFailureCode
}

// PreparedNativeMutation is a single native request assembled from one fresh
// guarded snapshot. Dispatch must be called only after durable admission has
// committed; it performs exactly one HTTP mutation attempt.
type PreparedNativeMutation struct {
	adapter *NativeAdapter
	kind    nativeMutationKind
	name    string
	body    []byte
	noop    bool
}

func (m *PreparedNativeMutation) Noop() bool { return m != nil && m.noop }

func (m *PreparedNativeMutation) Dispatch(ctx context.Context) (NativeMutationOutcome, error) {
	if m == nil || m.adapter == nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: NativeFailureNodeManagementUnavailable}, errors.New("native mutation unavailable")
	}
	return m.adapter.mutate(ctx, m.kind, m.name, m.body)
}

type NativeAuthFile struct {
	Name        string
	Provider    string
	Email       string
	Source      string
	RuntimeOnly bool
	AuthIndex   string
	Disabled    bool
	HasDisabled bool
	nameSet     bool
	sourceSet   bool
	authSet     bool
}

type NativeSnapshot struct {
	Files             []NativeAuthFile
	Version           string
	Commit            string
	Degraded          bool
	FailureCode       NativeFailureCode
	MutationEligible  []NativeAuthFile
	OccupancyEvidence []NativeAuthFile
}

type NativeAdapter struct {
	transport     *safeTransport
	managementKey string
	version       string
	commit        string
}

func (a *NativeAdapter) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED NativeAdapter]"))
}

// NewNativeAdapter creates the fixed CLIProxyAPI auth-files adapter. The key is
// retained only for the lifetime of this adapter and is never included in an
// error or response projection.
func NewNativeAdapter(endpoint string, config rootdrivers.ValidatedManagementConfig, managementKey string) (*NativeAdapter, error) {
	return newNativeAdapter(endpoint, config, managementKey, FrozenRuntimeVersion, FrozenRuntimeCommit, transportOptions{})
}

func newNativeAdapter(endpoint string, config rootdrivers.ValidatedManagementConfig, managementKey, version, commit string, options transportOptions) (*NativeAdapter, error) {
	if managementKey == "" || version == "" || commit == "" {
		return nil, &RequestError{Operation: OperationInventory, Reason: FailureRequestRejected}
	}
	transport, err := newTransport(endpoint, config, options)
	if err != nil {
		return nil, err
	}
	return &NativeAdapter{transport: transport, managementKey: managementKey, version: version, commit: commit}, nil
}

// SnapshotAuthFiles performs one fresh, authenticated GET and verifies the
// exact runtime identity before interpreting its body. Returned values are a
// bounded transient projection only.
func (a *NativeAdapter) SnapshotAuthFiles(ctx context.Context) (NativeSnapshot, error) {
	if a == nil || a.transport == nil || ctx == nil {
		return NativeSnapshot{FailureCode: NativeFailureNodeManagementUnavailable}, errors.New("native snapshot unavailable")
	}
	response, err := a.transport.getAccountInventory(ctx, a.managementKey)
	if err != nil {
		return NativeSnapshot{FailureCode: NativeFailureNodeManagementUnavailable}, err
	}
	defer response.Body.Close()
	version, commit, ok := exactRuntimeIdentity(response.Header, a.version, a.commit)
	if !ok {
		return NativeSnapshot{Version: version, Commit: commit, FailureCode: NativeFailureUnsupportedNodeVersion}, errors.New(string(NativeFailureUnsupportedNodeVersion))
	}
	if response.StatusCode != http.StatusOK {
		return NativeSnapshot{Version: version, Commit: commit, FailureCode: NativeFailureNodeManagementUnavailable}, errors.New(string(NativeFailureNodeManagementUnavailable))
	}
	body, tooLarge, readErr := readBounded(response.Body, a.transport.inventoryLimit)
	if tooLarge || readErr != nil {
		return NativeSnapshot{Version: version, Commit: commit, Degraded: true, FailureCode: NativeFailureNodeManagementUnavailable}, errors.New(string(NativeFailureNodeManagementUnavailable))
	}
	snapshot, err := parseNativeSnapshot(body, version, commit)
	if err != nil {
		return NativeSnapshot{Version: version, Commit: commit, Degraded: true, FailureCode: NativeFailureNodeManagementUnavailable}, err
	}
	return snapshot, nil
}

func exactRuntimeIdentity(headers http.Header, expectedVersion, expectedCommit string) (string, string, bool) {
	versions, commits := headers.Values("X-CPA-VERSION"), headers.Values("X-CPA-COMMIT")
	if len(versions) != 1 || len(commits) != 1 || versions[0] == "" || commits[0] == "" {
		return "", "", false
	}
	return versions[0], commits[0], versions[0] == expectedVersion && commits[0] == expectedCommit
}

func parseNativeSnapshot(encoded []byte, version, commit string) (NativeSnapshot, error) {
	top, err := decodeJSONObject(encoded)
	if err != nil {
		return NativeSnapshot{}, errors.New("native snapshot invalid")
	}
	var records []json.RawMessage
	rawFiles, ok := top["files"]
	if !ok || json.Unmarshal(rawFiles, &records) != nil || records == nil {
		return NativeSnapshot{}, errors.New("native snapshot invalid")
	}
	snapshot := NativeSnapshot{Version: version, Commit: commit, Files: make([]NativeAuthFile, 0, len(records))}
	for _, raw := range records {
		file, err := decodeNativeAuthFile(raw)
		if err != nil {
			return NativeSnapshot{}, err
		}
		snapshot.Files = append(snapshot.Files, file)
		if usableOccupancyEvidence(file) {
			snapshot.OccupancyEvidence = append(snapshot.OccupancyEvidence, file)
		}
		if mutationEligible(file) {
			snapshot.MutationEligible = append(snapshot.MutationEligible, file)
		}
	}
	if len(snapshot.Files) > 0 {
		for _, file := range snapshot.Files {
			if file.Source == "" || (file.Source == "file" && (!file.nameSet || !file.authSet)) {
				snapshot.Degraded = true
				snapshot.FailureCode = NativeFailureNodeManagementUnavailable
				return snapshot, errors.New(string(NativeFailureNodeManagementUnavailable))
			}
		}
	}
	return snapshot, nil
}

func decodeNativeAuthFile(raw []byte) (NativeAuthFile, error) {
	fields, err := decodeJSONObject(raw)
	if err != nil {
		return NativeAuthFile{}, errors.New("native snapshot invalid")
	}
	readString := func(key string) (string, bool, error) {
		value, present := fields[key]
		if !present {
			return "", false, nil
		}
		var result string
		if json.Unmarshal(value, &result) != nil || !validNativeScalar(result, 320) {
			return "", false, errors.New("native snapshot invalid")
		}
		return strings.TrimSpace(result), true, nil
	}
	name, _, err := readString("name")
	if err != nil {
		return NativeAuthFile{}, err
	}
	provider, providerSet, err := readString("provider")
	if err != nil {
		return NativeAuthFile{}, err
	}
	typ, typeSet, err := readString("type")
	if err != nil {
		return NativeAuthFile{}, err
	}
	if providerSet && typeSet && !strings.EqualFold(provider, typ) {
		return NativeAuthFile{}, errors.New("native snapshot invalid")
	}
	if !providerSet {
		provider = typ
		providerSet = typeSet
	}
	email, _, err := readString("email")
	if err != nil {
		return NativeAuthFile{}, err
	}
	source, sourceSet, err := readString("source")
	if err != nil {
		return NativeAuthFile{}, err
	}
	authIndex, authSet, err := readString("auth_index")
	if err != nil {
		return NativeAuthFile{}, err
	}
	var runtimeOnly, disabled bool
	var runtimeSet, disabledSet bool
	if value, present := fields["runtime_only"]; present {
		if json.Unmarshal(value, &runtimeOnly) != nil {
			return NativeAuthFile{}, errors.New("native snapshot invalid")
		}
		runtimeSet = true
	}
	if value, present := fields["disabled"]; present {
		if json.Unmarshal(value, &disabled) != nil {
			return NativeAuthFile{}, errors.New("native snapshot invalid")
		}
		disabledSet = true
	}
	if name == "" || !providerSet || !emailSet(email) || !sourceSet || !authSet || !runtimeSet || !disabledSet {
		// A valid disk-fallback shape is retained as occupancy evidence only;
		// it cannot be used as a mutation target.
		if !providerSet || !emailSet(email) {
			return NativeAuthFile{}, errors.New("native snapshot invalid")
		}
	}
	return NativeAuthFile{Name: name, Provider: strings.ToLower(provider), Email: strings.ToLower(email), Source: strings.ToLower(source), RuntimeOnly: runtimeOnly, AuthIndex: authIndex, Disabled: disabled, HasDisabled: disabledSet, nameSet: name != "", sourceSet: sourceSet, authSet: authSet}, nil
}

func emailSet(value string) bool { return value != "" }

func validNativeScalar(value string, max int) bool {
	return utf8.ValidString(value) && len(value) <= max && strings.IndexFunc(value, unicode.IsControl) < 0
}

func mutationEligible(file NativeAuthFile) bool {
	return file.Source == "file" && !file.RuntimeOnly && validNativeBasename(file.Name) && validAuthIndex(file.AuthIndex) && file.Provider == "antigravity" && file.Email != ""
}

func usableOccupancyEvidence(file NativeAuthFile) bool {
	return file.Provider != "" && file.Email != "" && (file.Name == "" || validNativeBasename(file.Name))
}

func validAuthIndex(value string) bool {
	return value != "" && len(value) <= 256 && validNativeScalar(value, 256)
}

func validNativeBasename(value string) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= 255 && safeNativeBasename.MatchString(value) && value != "." && value != ".." && !strings.Contains(value, "..") && strings.IndexFunc(value, unicode.IsControl) < 0
}

// ValidUploadNewIdentity validates the deterministic identity-to-basename
// mapping before any snapshot or mutation work is performed.
func ValidUploadNewIdentity(provider, email string) bool {
	provider, email = strings.ToLower(strings.TrimSpace(provider)), strings.ToLower(strings.TrimSpace(email))
	return provider == "antigravity" && email != "" && len(email) <= maxCreateEmailBytes && validNativeBasename(nativeUploadPrefix+email+nativeUploadSuffix)
}

// ResolveMutationTarget uses only the supplied fresh snapshot and never
// chooses an arbitrary first/latest record.
func ResolveMutationTarget(snapshot NativeSnapshot, provider, email string) (NativeAuthFile, NativeFailureCode, error) {
	if snapshot.Degraded || snapshot.FailureCode != "" {
		return NativeAuthFile{}, NativeFailureNodeManagementUnavailable, errors.New(string(NativeFailureNodeManagementUnavailable))
	}
	provider, email = strings.ToLower(strings.TrimSpace(provider)), strings.ToLower(strings.TrimSpace(email))
	var matches []NativeAuthFile
	for _, file := range snapshot.MutationEligible {
		if file.Provider == provider && file.Email == email {
			matches = append(matches, file)
		}
	}
	switch len(matches) {
	case 0:
		return NativeAuthFile{}, NativeFailureTargetNotFound, errors.New(string(NativeFailureTargetNotFound))
	case 1:
		return matches[0], "", nil
	default:
		return NativeAuthFile{}, NativeFailureTargetAmbiguous, errors.New(string(NativeFailureTargetAmbiguous))
	}
}

// ClassifyUploadAdmission keeps broader occupancy evidence separate from
// mutation eligibility. It does not issue a request or mutate state.
func ClassifyUploadAdmission(snapshot NativeSnapshot, provider, email, basename string) NativeFailureCode {
	if snapshot.Degraded || snapshot.FailureCode != "" {
		return NativeFailureNodeManagementUnavailable
	}
	provider, email = strings.ToLower(strings.TrimSpace(provider)), strings.ToLower(strings.TrimSpace(email))
	for _, file := range snapshot.OccupancyEvidence {
		if file.Provider == provider && file.Email == email {
			return NativeFailureTargetExists
		}
		if file.Name == basename {
			return NativeFailureFilenameConflict
		}
	}
	return ""
}

func (a *NativeAdapter) SetAuthFileDisabled(ctx context.Context, provider, email string, disabled bool) (NativeMutationOutcome, error) {
	mutation, err := a.PrepareAuthFileDisabled(ctx, provider, email, disabled)
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: failureCodeFromError(err)}, err
	}
	if mutation.Noop() {
		return NativeMutationOutcome{Kind: NativeOutcomeNoop}, nil
	}
	return mutation.Dispatch(ctx)
}

func (a *NativeAdapter) DisableAuthFile(ctx context.Context, provider, email string) (NativeMutationOutcome, error) {
	return a.SetAuthFileDisabled(ctx, provider, email, true)
}

func (a *NativeAdapter) EnableAuthFile(ctx context.Context, provider, email string) (NativeMutationOutcome, error) {
	return a.SetAuthFileDisabled(ctx, provider, email, false)
}

func (a *NativeAdapter) DeleteAuthFile(ctx context.Context, provider, email string) (NativeMutationOutcome, error) {
	mutation, err := a.PrepareDeleteAuthFile(ctx, provider, email)
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: failureCodeFromError(err)}, err
	}
	return mutation.Dispatch(ctx)
}

// UploadAuthFile performs Upload New. The physical basename is derived from
// the logical identity and is never accepted from the caller.
func (a *NativeAdapter) UploadAuthFile(ctx context.Context, provider, email string, credential []byte) (NativeMutationOutcome, error) {
	mutation, err := a.PrepareUploadAuthFile(ctx, provider, email, credential)
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: failureCodeFromError(err)}, err
	}
	return mutation.Dispatch(ctx)
}

func (a *NativeAdapter) PrepareAuthFileDisabled(ctx context.Context, provider, email string, disabled bool) (*PreparedNativeMutation, error) {
	snapshot, err := a.SnapshotAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	target, code, err := ResolveMutationTarget(snapshot, provider, email)
	if err != nil {
		return nil, nativeFailureError(code)
	}
	body, err := json.Marshal(struct {
		Name      string `json:"name"`
		AuthIndex string `json:"auth_index"`
		Disabled  bool   `json:"disabled"`
	}{target.Name, target.AuthIndex, disabled})
	if err != nil {
		return nil, err
	}
	return &PreparedNativeMutation{adapter: a, kind: nativeMutationStatus, body: body, noop: target.Disabled == disabled}, nil
}

func (a *NativeAdapter) PrepareDeleteAuthFile(ctx context.Context, provider, email string) (*PreparedNativeMutation, error) {
	snapshot, err := a.SnapshotAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	target, code, err := ResolveMutationTarget(snapshot, provider, email)
	if err != nil {
		return nil, nativeFailureError(code)
	}
	return &PreparedNativeMutation{adapter: a, kind: nativeMutationDelete, name: target.Name}, nil
}

func (a *NativeAdapter) PrepareUploadAuthFile(ctx context.Context, provider, email string, credential []byte) (*PreparedNativeMutation, error) {
	provider, email = strings.ToLower(strings.TrimSpace(provider)), strings.ToLower(strings.TrimSpace(email))
	name := nativeUploadPrefix + email + nativeUploadSuffix
	if !ValidUploadNewIdentity(provider, email) || len(credential) == 0 || int64(len(credential)) > 1<<20 {
		return nil, nativeFailureError(NativeFailureInvalidRequest)
	}
	snapshot, err := a.SnapshotAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	if code := ClassifyUploadAdmission(snapshot, provider, email, name); code != "" {
		return nil, nativeFailureError(code)
	}
	return &PreparedNativeMutation{adapter: a, kind: nativeMutationUpload, name: name, body: append([]byte(nil), credential...)}, nil
}

// ReplaceAuthFile performs Replace Existing. The physical basename is
// resolved from this call's fresh snapshot and is never caller-supplied.
func (a *NativeAdapter) ReplaceAuthFile(ctx context.Context, provider, email string, credential []byte) (NativeMutationOutcome, error) {
	mutation, err := a.PrepareReplaceAuthFile(ctx, provider, email, credential)
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: failureCodeFromError(err)}, err
	}
	return mutation.Dispatch(ctx)
}

func (a *NativeAdapter) PrepareReplaceAuthFile(ctx context.Context, provider, email string, credential []byte) (*PreparedNativeMutation, error) {
	if len(credential) == 0 || int64(len(credential)) > 1<<20 {
		return nil, nativeFailureError(NativeFailureInvalidRequest)
	}
	snapshot, err := a.SnapshotAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	target, code, err := ResolveMutationTarget(snapshot, provider, email)
	if err != nil {
		return nil, nativeFailureError(code)
	}
	return &PreparedNativeMutation{adapter: a, kind: nativeMutationUpload, name: target.Name, body: append([]byte(nil), credential...)}, nil
}

func nativeFailureError(code NativeFailureCode) error {
	if code == "" {
		code = NativeFailureNodeManagementUnavailable
	}
	return errors.New(string(code))
}

func failureCodeFromError(err error) NativeFailureCode {
	if err == nil {
		return ""
	}
	code := NativeFailureCode(err.Error())
	switch code {
	case NativeFailureUnsupportedNodeVersion, NativeFailureNodeManagementUnavailable,
		NativeFailureTargetNotFound, NativeFailureTargetAmbiguous, NativeFailureTargetExists,
		NativeFailureFilenameConflict, NativeFailureInvalidRequest:
		return code
	default:
		return NativeFailureNodeManagementUnavailable
	}
}

func snapshotFailure(snapshot NativeSnapshot, err error) (NativeMutationOutcome, error) {
	code := snapshot.FailureCode
	if code == "" {
		code = NativeFailureNodeManagementUnavailable
	}
	return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: code}, err
}

type nativeMutationKind uint8

const (
	nativeMutationStatus nativeMutationKind = iota + 1
	nativeMutationDelete
	nativeMutationUpload
)

func (a *NativeAdapter) mutate(ctx context.Context, kind nativeMutationKind, name string, body []byte) (NativeMutationOutcome, error) {
	method, path := "", ""
	switch kind {
	case nativeMutationStatus:
		method, path = http.MethodPatch, "/v0/management/auth-files/status"
	case nativeMutationDelete:
		method, path = http.MethodDelete, "/v0/management/auth-files"
	case nativeMutationUpload:
		method, path = http.MethodPost, "/v0/management/auth-files"
	default:
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: NativeFailureInvalidRequest}, errors.New("native operation rejected")
	}
	endpoint, err := validateEndpoint(a.transport.endpointRaw)
	if err != nil || endpoint != a.transport.endpoint {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: NativeFailureNodeManagementUnavailable}, errors.New("native endpoint rejected")
	}
	requestURL := endpointURL(endpoint, path, name)
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: NativeFailureNodeManagementUnavailable}, err
	}
	request.Header.Set("User-Agent", "relay-station-control/cliproxyapi-native")
	request.Header.Set("X-Management-Key", a.managementKey)
	if kind == nativeMutationStatus || kind == nativeMutationUpload {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.transport.client.Do(request)
	if err != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeUnknown}, classifyRequestError(OperationInventory, ctx, err)
	}
	defer response.Body.Close()
	if _, tooLarge, readErr := readBounded(response.Body, nativeMutationBodyLimit); tooLarge || readErr != nil {
		return NativeMutationOutcome{Kind: NativeOutcomeUnknown}, errors.New("native mutation response unavailable")
	}
	return classifyNativeResponse(kind, response.StatusCode), nil
}

func endpointURL(endpoint validatedEndpoint, path, name string) string {
	base := "http://" + endpoint.hostname + ":" + endpoint.port + endpoint.basePath + path
	if name == "" {
		return base
	}
	return base + "?name=" + urlQueryEscape(name)
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func classifyNativeResponse(kind nativeMutationKind, status int) NativeMutationOutcome {
	if status >= 200 && status < 300 {
		return NativeMutationOutcome{Kind: NativeOutcomeApplied}
	}
	if kind == nativeMutationUpload && status == http.StatusServiceUnavailable {
		return NativeMutationOutcome{Kind: NativeOutcomeFailed, FailureCode: NativeFailureNodeManagementUnavailable}
	}
	return NativeMutationOutcome{Kind: NativeOutcomeUnknown}
}
