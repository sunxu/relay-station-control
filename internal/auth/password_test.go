package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestValidatePasswordUnicodeBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		password string
		login    string
		wantErr  bool
	}{
		{name: "13 runes", password: strings.Repeat("界", 13), wantErr: true},
		{name: "14 runes", password: strings.Repeat("界", 14)},
		{name: "128 runes", password: strings.Repeat("界", 128)},
		{name: "129 runes", password: strings.Repeat("界", 129), wantErr: true},
		{name: "spaces and symbols", password: "  valid pass!  ", login: "operator"},
		{name: "same as normalized login", password: "operator-name1", login: "operator-name1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePassword(test.password, test.login)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePassword() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestArgon2idPHCRoundTripAndUpgrade(t *testing.T) {
	password := "correct horse battery staple"
	salt := bytes.Repeat([]byte{0x42}, int(CurrentArgon2Params.SaltLength))
	phc, err := HashPasswordWith(bytes.NewReader(salt), password, CurrentArgon2Params)
	if err != nil {
		t.Fatal(err)
	}
	want := "$argon2id$v=19$m=65536,t=3,p=2$QkJCQkJCQkJCQkJCQkJCQg$xxS7sjj9EeBfBO6CKPnr4+5cItXPwZPNvIGag1Lk5ps"
	if phc != want {
		t.Fatalf("PHC vector changed:\n got %s\nwant %s", phc, want)
	}
	match, upgrade, err := VerifyPassword(password, phc)
	if err != nil || !match || upgrade {
		t.Fatalf("VerifyPassword() = %v, %v, %v", match, upgrade, err)
	}
	match, _, err = VerifyPassword("wrong password", phc)
	if err != nil || match {
		t.Fatalf("wrong VerifyPassword() = %v, %v", match, err)
	}
}

func TestArgon2idUsesIndependentSaltsAndRejectsMalformedPHC(t *testing.T) {
	first, err := HashPassword("a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password hashes reused a salt")
	}
	malformed := []string{"", "$argon2i$v=19$m=65536,t=3,p=2$bad$bad", "$argon2id$v=19$m=1,t=1,p=0$YQ$YQ", first + "$trailing"}
	for _, value := range malformed {
		if _, _, err := VerifyPassword("password", value); err == nil {
			t.Fatalf("VerifyPassword(%q) accepted malformed PHC", value)
		}
	}
}

func TestDummyPasswordHashUsesCurrentParameters(t *testing.T) {
	phc, err := NewDummyPasswordHash()
	if err != nil {
		t.Fatal(err)
	}
	params, _, _, err := parseArgon2PHC(phc)
	if err != nil || params != CurrentArgon2Params {
		t.Fatalf("dummy parameters = %#v, %v", params, err)
	}
}
