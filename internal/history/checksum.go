package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"
)

const ChecksumVersionV1 = uint16(1)

var (
	ErrCanonicalFieldTooLarge = errors.New("history: canonical field too large")
	ErrCanonicalTextInvalid   = errors.New("history: canonical text is not UTF-8")
	ErrCanonicalRowInvalid    = errors.New("history: canonical row is invalid")
)

var checksumRowMagicV1 = [4]byte{'R', 'S', 'H', '1'}

// CanonicalField is constructed only through the typed helpers below. A fixed
// row schema supplies field type and position, while this value distinguishes
// SQL NULL from an empty value.
type CanonicalField struct {
	value  []byte
	isNull bool
}

func NullField() CanonicalField { return CanonicalField{isNull: true} }

func TextField(value string) CanonicalField {
	return CanonicalField{value: []byte(value)}
}

func BytesField(value []byte) CanonicalField {
	encoded := make([]byte, hex.EncodedLen(len(value)))
	hex.Encode(encoded, value)
	return CanonicalField{value: encoded}
}

func Int64Field(value int64) CanonicalField {
	return CanonicalField{value: strconv.AppendInt(nil, value, 10)}
}

func Uint64Field(value uint64) CanonicalField {
	return CanonicalField{value: strconv.AppendUint(nil, value, 10)}
}

func BoolField(value bool) CanonicalField {
	if value {
		return CanonicalField{value: []byte{'t'}}
	}
	return CanonicalField{value: []byte{'f'}}
}

func TimeField(value time.Time) CanonicalField {
	return TextField(value.UTC().Format(time.RFC3339Nano))
}

// CanonicalRowV1 encodes a row as RSH1, a big-endian field count, and fields
// prefixed by a big-endian byte length. 0xffffffff represents SQL NULL. Text,
// numbers, booleans, timestamps, and byte strings use the canonical forms from
// the typed constructors above.
func CanonicalRowV1(fields ...CanonicalField) ([]byte, error) {
	if uint64(len(fields)) > uint64(^uint32(0)) {
		return nil, ErrCanonicalFieldTooLarge
	}
	size := 8
	for _, field := range fields {
		if field.isNull {
			size += 4
			continue
		}
		if !utf8.Valid(field.value) {
			return nil, ErrCanonicalTextInvalid
		}
		if uint64(len(field.value)) >= uint64(^uint32(0)) {
			return nil, ErrCanonicalFieldTooLarge
		}
		size += 4 + len(field.value)
	}
	result := make([]byte, 8, size)
	copy(result, checksumRowMagicV1[:])
	binary.BigEndian.PutUint32(result[4:], uint32(len(fields)))
	for _, field := range fields {
		var prefix [4]byte
		if field.isNull {
			binary.BigEndian.PutUint32(prefix[:], ^uint32(0))
			result = append(result, prefix[:]...)
			continue
		}
		binary.BigEndian.PutUint32(prefix[:], uint32(len(field.value)))
		result = append(result, prefix[:]...)
		result = append(result, field.value...)
	}
	return result, nil
}

// ChainV1 computes H0=zero32, Ri=sha256(canonical row), and
// Hi=sha256(Hi-1 || Ri). It retains no row data, so memory is constant when a
// caller feeds a stable ordered cursor.
type ChainV1 struct {
	sum [sha256.Size]byte
}

// AddCanonicalRow accepts only a structurally complete CanonicalRowV1 value.
// Validation mirrors the SQL helper's RSH1 boundary and prevents callers from
// silently chaining arbitrary or truncated bytes under checksum version one.
func (chain *ChainV1) AddCanonicalRow(row []byte) error {
	if !validCanonicalRowV1(row) {
		return ErrCanonicalRowInvalid
	}
	rowHash := sha256.Sum256(row)
	var input [sha256.Size * 2]byte
	copy(input[:sha256.Size], chain.sum[:])
	copy(input[sha256.Size:], rowHash[:])
	chain.sum = sha256.Sum256(input[:])
	return nil
}

func (chain *ChainV1) Add(fields ...CanonicalField) error {
	row, err := CanonicalRowV1(fields...)
	if err != nil {
		return err
	}
	return chain.AddCanonicalRow(row)
}

func (chain *ChainV1) Sum() [sha256.Size]byte { return chain.sum }

func validCanonicalRowV1(row []byte) bool {
	if len(row) < 8 || !bytes.Equal(row[:4], checksumRowMagicV1[:]) {
		return false
	}
	fieldCount := binary.BigEndian.Uint32(row[4:8])
	remaining := row[8:]
	for range fieldCount {
		if len(remaining) < 4 {
			return false
		}
		length := binary.BigEndian.Uint32(remaining[:4])
		remaining = remaining[4:]
		if length == ^uint32(0) {
			continue
		}
		if uint64(length) > uint64(len(remaining)) {
			return false
		}
		remaining = remaining[int(length):]
	}
	return len(remaining) == 0
}
