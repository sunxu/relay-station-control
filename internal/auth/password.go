package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	PasswordMinRunes = 14
	PasswordMaxRunes = 128
)

type Argon2Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var CurrentArgon2Params = Argon2Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

func ValidatePassword(password, normalizedLogin string) error {
	if !utf8.ValidString(password) {
		return errors.New("auth: password must be valid UTF-8")
	}
	length := utf8.RuneCountInString(password)
	if length < PasswordMinRunes || length > PasswordMaxRunes {
		return fmt.Errorf("auth: password length must be between %d and %d Unicode characters", PasswordMinRunes, PasswordMaxRunes)
	}
	if password == strings.ToLower(normalizedLogin) {
		return errors.New("auth: password must differ from the normalized login name")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	return HashPasswordWith(rand.Reader, password, CurrentArgon2Params)
}

func HashPasswordWith(random io.Reader, password string, params Argon2Params) (string, error) {
	if err := validateArgon2Params(params); err != nil {
		return "", err
	}
	if !utf8.ValidString(password) {
		return "", errors.New("auth: password must be valid UTF-8")
	}
	salt := make([]byte, params.SaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", errors.New("auth: generate password salt")
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.Memory, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(password, phc string) (match bool, needsUpgrade bool, err error) {
	params, salt, expected, err := parseArgon2PHC(phc)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	match = subtle.ConstantTimeCompare(actual, expected) == 1
	return match, match && params != CurrentArgon2Params, nil
}

func NewDummyPasswordHash() (string, error) {
	// The random value is never a user credential; it keeps the unknown-account path
	// on the same Argon2id parameters as a real password verification.
	dummy := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dummy); err != nil {
		return "", errors.New("auth: generate dummy password")
	}
	return HashPassword(base64.RawURLEncoding.EncodeToString(dummy))
}

func validateArgon2Params(params Argon2Params) error {
	if params.Memory < 64*1024 || params.Iterations < 3 || params.Parallelism == 0 || params.SaltLength < 16 || params.KeyLength < 32 {
		return errors.New("auth: Argon2id parameters are below the security floor")
	}
	if params.Memory > 1024*1024 || params.Iterations > 10 || params.Parallelism > 16 || params.SaltLength > 64 || params.KeyLength > 64 {
		return errors.New("auth: Argon2id parameters exceed supported limits")
	}
	return nil
}

func parseArgon2PHC(phc string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Argon2Params{}, nil, nil, errors.New("auth: malformed Argon2id PHC string")
	}
	var params Argon2Params
	if count, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &params.Parallelism); err != nil || count != 3 || parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", params.Memory, params.Iterations, params.Parallelism) {
		return Argon2Params{}, nil, nil, errors.New("auth: malformed Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, errors.New("auth: malformed Argon2id salt")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Argon2Params{}, nil, nil, errors.New("auth: malformed Argon2id hash")
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(expected))
	if err := validateArgon2Params(params); err != nil {
		return Argon2Params{}, nil, nil, err
	}
	return params, salt, expected, nil
}
