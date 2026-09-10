package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

type FieldType string

const (
	FieldString      FieldType = "string"
	FieldBoolean     FieldType = "boolean"
	FieldInteger     FieldType = "integer"
	FieldUUID        FieldType = "uuid"
	FieldStringArray FieldType = "string_array"
)

type Field struct {
	Type      FieldType
	Required  bool
	MinLength int
	MaxLength int
	Pattern   *regexp.Regexp
	MaxItems  int
	// AllowDuplicates preserves repeated display values in string arrays.
	// The default retains the existing sorted, unique collection contract.
	AllowDuplicates bool
	// PreserveOrder opts out of lexical ordering for positional display arrays.
	// It does not relax uniqueness unless AllowDuplicates is also enabled.
	PreserveOrder bool
}

type Schema struct {
	Fields map[string]Field
}

type Definition struct {
	Kind          string
	SchemaVersion int
	Schema        Schema
	// ValidatePayload optionally checks kind-specific semantics after schema
	// canonicalization and before hashing/enqueue. It must not mutate the payload.
	ValidatePayload          func([]byte) error
	Timeout                  time.Duration
	LeaseDuration            time.Duration
	HeartbeatInterval        time.Duration
	MaxAttempts              int
	MaxVerifyAttempts        int
	AllowRollback            bool
	ReplaySafe               bool
	AllowUnknownEffectReplay bool
	AllowDirectSuccess       bool
	ErrorCodes               map[string]struct{}
	Executor                 Executor
}

type Registry struct {
	definitions map[string]Definition
}

var (
	kindPattern        = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	fieldPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	secretFieldPattern = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|authorization|cookie|private.?key|api.?key|raw.?response|command)`)
)

func NewProductionRegistry() *Registry { return &Registry{definitions: map[string]Definition{}} }

func NewRegistry(definitions ...Definition) (*Registry, error) {
	registry := NewProductionRegistry()
	for _, definition := range definitions {
		if err := registry.register(definition); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) register(def Definition) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if !kindPattern.MatchString(def.Kind) || def.SchemaVersion < 1 || def.Executor == nil ||
		def.Timeout < time.Second || def.Timeout > 24*time.Hour || def.Timeout%time.Second != 0 ||
		def.LeaseDuration < 5*time.Second || def.LeaseDuration > time.Hour || def.LeaseDuration%time.Second != 0 ||
		def.HeartbeatInterval < time.Second || def.HeartbeatInterval >= def.LeaseDuration || def.HeartbeatInterval%time.Second != 0 ||
		def.MaxAttempts < 1 || def.MaxAttempts > 100 || def.MaxVerifyAttempts < 1 || def.MaxVerifyAttempts > 100 {
		return fmt.Errorf("invalid job definition")
	}
	if def.AllowUnknownEffectReplay && !def.ReplaySafe {
		return fmt.Errorf("invalid execution policy: unknown-effect replay requires replay-safe")
	}
	if _, exists := r.definitions[def.Kind]; exists {
		return fmt.Errorf("duplicate job kind")
	}
	for name, field := range def.Schema.Fields {
		if (field.AllowDuplicates || field.PreserveOrder) && field.Type != FieldStringArray {
			return fmt.Errorf("invalid payload schema")
		}
		if !fieldPattern.MatchString(name) || secretFieldPattern.MatchString(name) || !field.Type.valid() {
			return fmt.Errorf("invalid payload schema")
		}
		if field.MinLength < 0 || field.MaxLength < field.MinLength || field.MaxLength > MaxPayloadBytes || field.MaxItems < 0 || field.MaxItems > 1024 {
			return fmt.Errorf("invalid payload schema")
		}
	}
	r.definitions[def.Kind] = cloneDefinition(def)
	return nil
}

func (t FieldType) valid() bool {
	return t == FieldString || t == FieldBoolean || t == FieldInteger || t == FieldUUID || t == FieldStringArray
}

func (r *Registry) Lookup(kind string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.definitions[kind]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(definition), true
}

func cloneDefinition(definition Definition) Definition {
	clone := definition
	clone.Schema.Fields = make(map[string]Field, len(definition.Schema.Fields))
	for name, field := range definition.Schema.Fields {
		clone.Schema.Fields[name] = field
	}
	clone.ErrorCodes = make(map[string]struct{}, len(definition.ErrorCodes))
	for code := range definition.ErrorCodes {
		clone.ErrorCodes[code] = struct{}{}
	}
	return clone
}

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.definitions)
}

// CatalogEntry is the exact, executor-free database registration contract for
// a job kind. Catalog returns entries in deterministic kind/schema order so the
// composition root can compare the complete database and process catalogs.
type CatalogEntry struct {
	Kind                     string
	SchemaVersion            int
	Timeout                  time.Duration
	LeaseDuration            time.Duration
	HeartbeatInterval        time.Duration
	MaxAttempts              int
	MaxVerifyAttempts        int
	ReplaySafe               bool
	AllowUnknownEffectReplay bool
	AllowDirectSuccess       bool
	AllowRollback            bool
}

func (r *Registry) Catalog() []CatalogEntry {
	if r == nil {
		return []CatalogEntry{}
	}
	entries := make([]CatalogEntry, 0, len(r.definitions))
	for _, definition := range r.definitions {
		entries = append(entries, CatalogEntry{
			Kind: definition.Kind, SchemaVersion: definition.SchemaVersion,
			Timeout: definition.Timeout, LeaseDuration: definition.LeaseDuration,
			HeartbeatInterval: definition.HeartbeatInterval,
			MaxAttempts:       definition.MaxAttempts, MaxVerifyAttempts: definition.MaxVerifyAttempts,
			ReplaySafe: definition.ReplaySafe, AllowUnknownEffectReplay: definition.AllowUnknownEffectReplay,
			AllowDirectSuccess: definition.AllowDirectSuccess, AllowRollback: definition.AllowRollback,
		})
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Kind != entries[right].Kind {
			return entries[left].Kind < entries[right].Kind
		}
		return entries[left].SchemaVersion < entries[right].SchemaVersion
	})
	return entries
}

func (r *Registry) ValidateAndHash(kind string, schemaVersion int, raw []byte) ([]byte, [32]byte, Definition, error) {
	definition, ok := r.Lookup(kind)
	if !ok {
		return nil, [32]byte{}, Definition{}, ErrUnknownKind
	}
	if schemaVersion != definition.SchemaVersion {
		return nil, [32]byte{}, Definition{}, fmt.Errorf("%w: schema version", ErrInvalidPayload)
	}
	canonical, err := definition.Schema.Canonicalize(raw)
	if err != nil {
		return nil, [32]byte{}, Definition{}, err
	}
	if definition.ValidatePayload != nil {
		if err := definition.ValidatePayload(bytes.Clone(canonical)); err != nil {
			return nil, [32]byte{}, Definition{}, ErrInvalidPayload
		}
	}
	return canonical, sha256.Sum256(canonical), definition, nil
}

func (s Schema) Canonicalize(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return nil, ErrInvalidPayload
	}
	if err := rejectDuplicateObjectKeys(raw); err != nil {
		return nil, ErrInvalidPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrInvalidPayload
	}
	if trailingErr := decoder.Decode(new(any)); trailingErr != io.EOF {
		return nil, ErrInvalidPayload
	}
	for name := range object {
		if secretFieldPattern.MatchString(name) {
			return nil, ErrInvalidPayload
		}
		if _, ok := s.Fields[name]; !ok {
			return nil, ErrInvalidPayload
		}
	}
	for name, field := range s.Fields {
		value, present := object[name]
		if !present {
			if field.Required {
				return nil, ErrInvalidPayload
			}
			continue
		}
		normalized, err := field.normalize(value)
		if err != nil {
			return nil, ErrInvalidPayload
		}
		object[name] = normalized
	}
	canonical, err := json.Marshal(object)
	if err != nil || len(canonical) > MaxPayloadBytes {
		return nil, ErrInvalidPayload
	}
	return canonical, nil
}

func (f Field) normalize(value any) (any, error) {
	switch f.Type {
	case FieldString:
		text, ok := value.(string)
		if !ok || !f.validText(text) {
			return nil, ErrInvalidPayload
		}
		return text, nil
	case FieldUUID:
		text, ok := value.(string)
		parsed, err := uuid.Parse(text)
		if !ok || err != nil || parsed == uuid.Nil {
			return nil, ErrInvalidPayload
		}
		return parsed.String(), nil
	case FieldBoolean:
		boolean, ok := value.(bool)
		if !ok {
			return nil, ErrInvalidPayload
		}
		return boolean, nil
	case FieldInteger:
		number, ok := value.(json.Number)
		if !ok {
			return nil, ErrInvalidPayload
		}
		integer, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return nil, ErrInvalidPayload
		}
		return integer, nil
	case FieldStringArray:
		values, ok := value.([]any)
		if !ok || f.MaxItems > 0 && len(values) > f.MaxItems {
			return nil, ErrInvalidPayload
		}
		result := make([]string, len(values))
		for index, value := range values {
			text, ok := value.(string)
			if !ok || !f.validText(text) {
				return nil, ErrInvalidPayload
			}
			result[index] = text
		}
		if !f.PreserveOrder && !sort.StringsAreSorted(result) {
			return nil, ErrInvalidPayload
		}
		if !f.AllowDuplicates {
			seen := make(map[string]struct{}, len(result))
			for _, text := range result {
				if _, exists := seen[text]; exists {
					return nil, ErrInvalidPayload
				}
				seen[text] = struct{}{}
			}
		}
		return result, nil
	default:
		return nil, ErrInvalidPayload
	}
}

func (f Field) validText(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	length := len(value)
	if length < f.MinLength || f.MaxLength > 0 && length > f.MaxLength {
		return false
	}
	return f.Pattern == nil || f.Pattern.MatchString(value)
}

func rejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return ErrInvalidPayload
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return ErrInvalidPayload
		}
		if _, duplicate := seen[name]; duplicate {
			return ErrInvalidPayload
		}
		seen[name] = struct{}{}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return ErrInvalidPayload
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return ErrInvalidPayload
	}
	if trailingErr := decoder.Decode(new(any)); trailingErr != io.EOF {
		return ErrInvalidPayload
	}
	return nil
}
