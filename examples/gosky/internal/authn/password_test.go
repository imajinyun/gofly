package authn

import (
	"strings"
	"testing"
)

// Failure-mode inventory:
//
//	PW-01 VerifyPassword accepts the matching plaintext and rejects wrong input.
//	PW-02 minimum length is enforced with complexity disabled.
//	PW-03 three-of-four classes are required with complexity enabled.
//	PW-04 length is capped at 20 runes when complexity is enabled.
func TestVerifyPassword(t *testing.T) {
	hash, err := HashPassword("Admin@12345")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := VerifyPassword("Admin@12345", hash); err != nil {
		t.Fatalf("verify correct password: %v", err)
	}
	if err := VerifyPassword("wrong-password", hash); err == nil {
		t.Fatal("wrong password unexpectedly accepted")
	}
	if err := VerifyPassword("Admin@12345", "not-a-phc"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestValidatePasswordComplexity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		password   string
		complexity bool
		wantErr    bool
	}{
		{name: "simple minimum length", password: "abcdefgh", complexity: false},
		{name: "simple too short", password: "abc", complexity: false, wantErr: true},
		{name: "strong passes", password: "Admin@12345", complexity: true},
		{name: "letters only rejected", password: "abcdefghij", complexity: true, wantErr: true},
		{name: "two classes rejected", password: "admin12345", complexity: true, wantErr: true},
		{name: "three classes pass", password: "Admin12345", complexity: true},
		{name: "special only with digits and lower pass", password: "admin@1234", complexity: true},
		{name: "too long", password: strings.Repeat("a", 21), complexity: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordComplexity(tc.password, tc.complexity)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidatePasswordComplexity(%q) err=%v wantErr=%v", tc.password, err, tc.wantErr)
			}
		})
	}
}
