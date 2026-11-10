package smartweb

import "testing"

// The ciphertext is produced by smart-web's `encryptData("correct-password")`
// with its fixed SM4 key and Base64 wrapping. Keeping this external fixture
// proves the Go adapter interoperates with the unmodified browser client.
func TestDecryptPasswordSmartWebSM4Fixture(t *testing.T) {
	const encrypted = "ODRmYjQ3YmM0ZWM4NjU4NjYyNGJkZDE2N2QwYjE5NmRmNjg4ZGZiNzRlMmU5MDg4ZjNkYWE2MGM3OGQyMTQ0Mg=="
	plain, err := DecryptPassword(encrypted)
	if err != nil {
		t.Fatalf("decrypt smart-web password: %v", err)
	}
	if plain != "correct-password" {
		t.Fatalf("plaintext = %q, want correct-password", plain)
	}
}
