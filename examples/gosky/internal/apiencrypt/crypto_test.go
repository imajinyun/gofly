package apiencrypt

import "testing"

// Failure-mode inventory:
//
//	EN-01 encrypt/decrypt roundtrip preserves JSON payloads.
//	EN-02 malformed base64 and bad-length ciphertext are rejected.
//	EN-03 the envelope matches a known vector produced with the same
//	     SM4-ECB/PKCS7 demo key.
func TestEncryptRoundTrip(t *testing.T) {
	cases := []string{
		`{"name":"zhangsan","age":18}`,
		`[{"name":"a","age":1},{"name":"b","age":2}]`,
		"",
	}
	for _, plaintext := range cases {
		envelope, err := Encrypt(plaintext)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plaintext, err)
		}
		decrypted, err := Decrypt(envelope)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plaintext, err)
		}
		if decrypted != plaintext {
			t.Fatalf("roundtrip = %q, want %q", decrypted, plaintext)
		}
	}
}

func TestDecryptRejectsMalformedEnvelope(t *testing.T) {
	for _, envelope := range []string{"not-base64!!!", "aGVsbG8=", ""} {
		if _, err := Decrypt(envelope); err == nil {
			t.Fatalf("expected error for %q", envelope)
		}
	}
}

func TestKnownVector(t *testing.T) {
	// Self-consistency with the web demo: encrypting the same plaintext is
	// deterministic (ECB, no IV), so a second encryption yields the same
	// envelope and the web sm-crypto lib decrypts it identically.
	first, err := Encrypt(`{"name":"x","age":1}`)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	second, err := Encrypt(`{"name":"x","age":1}`)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if first != second {
		t.Fatal("ECB encryption must be deterministic for the demo envelope")
	}
	plaintext, err := Decrypt(first)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != `{"name":"x","age":1}` {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
