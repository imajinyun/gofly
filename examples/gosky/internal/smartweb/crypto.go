// Package smartweb contains the narrow wire-compatibility boundary for the
// existing smart-web client. It is deliberately separate from the Gosky-native
// authentication API so new clients are not coupled to this legacy transport.
package smartweb

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	gmCipher "github.com/emmansun/gmsm/cipher"
	"github.com/emmansun/gmsm/padding"
	"github.com/emmansun/gmsm/sm4"
)

const smartWebSM4Key = "1024lab__1024lab"

// DecryptPassword accepts exactly smart-web's historical SM4-ECB/PKCS#7
// payload: Base64 encodes an ASCII lowercase hexadecimal ciphertext. This
// compatibility format is only a transport adapter; password verification
// remains Argon2id-based in authn.Service.
func DecryptPassword(value string) (string, error) {
	encoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("smart-web password encoding is invalid")
	}
	ciphertext, err := hex.DecodeString(string(encoded))
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%sm4.BlockSize != 0 {
		return "", errors.New("smart-web password ciphertext is invalid")
	}
	block, err := sm4.NewCipher([]byte(smartWebSM4Key))
	if err != nil {
		return "", fmt.Errorf("construct SM4 cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	gmCipher.NewECBDecrypter(block).CryptBlocks(plaintext, ciphertext)
	plaintext, err = padding.NewPKCS7Padding(sm4.BlockSize).ConstantTimeUnpad(plaintext)
	if err != nil {
		return "", errors.New("smart-web password padding is invalid")
	}
	if len(plaintext) == 0 || len([]rune(string(plaintext))) > 1024 {
		return "", errors.New("smart-web password plaintext is invalid")
	}
	return string(plaintext), nil
}

// DecryptEnvelope decrypts the postEncryptRequest body used by the unchanged
// client for arbitrary JSON forms (self-service password change etc.). The
// payload is SM4-ECB/PKCS7, Base64(hex ciphertext), same transport as
// DecryptPassword; the resulting plaintext is the original JSON document.
func DecryptEnvelope(value string) (string, error) {
	return DecryptPassword(value)
}
