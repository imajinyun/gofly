// Package apiencrypt implements the unmodified SmartAdmin API-encryption
// demo envelope: SM4-ECB/PKCS#7 with the shared demo key "1024lab__1024lab",
// transported as base64(hex-ciphertext), exactly like the web lib/encrypt.ts
// and the Java ApiEncryptServiceSmImpl.
package apiencrypt

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	gmCipher "github.com/emmansun/gmsm/cipher"
	"github.com/emmansun/gmsm/padding"
	"github.com/emmansun/gmsm/sm4"
)

const sm4Key = "1024lab__1024lab"

// Encrypt encrypts plaintext into the demo envelope.
func Encrypt(plaintext string) (string, error) {
	block, err := sm4.NewCipher([]byte(sm4Key))
	if err != nil {
		return "", fmt.Errorf("construct sm4 cipher: %w", err)
	}
	padder := padding.NewPKCS7Padding(sm4.BlockSize)
	ciphertext := make([]byte, len(plaintext)+sm4.BlockSize-len(plaintext)%sm4.BlockSize)
	gmCipher.NewECBEncrypter(block).CryptBlocks(ciphertext, padder.Pad([]byte(plaintext)))
	return base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(ciphertext))), nil
}

// Decrypt reverses the demo envelope.
func Decrypt(envelope string) (string, error) {
	hexCipher, err := base64.StdEncoding.DecodeString(envelope)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	ciphertext, err := hex.DecodeString(string(hexCipher))
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%sm4.BlockSize != 0 {
		return "", fmt.Errorf("sm4 ciphertext is invalid")
	}
	block, err := sm4.NewCipher([]byte(sm4Key))
	if err != nil {
		return "", fmt.Errorf("construct sm4 cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	gmCipher.NewECBDecrypter(block).CryptBlocks(plaintext, ciphertext)
	unpadded, err := padding.NewPKCS7Padding(sm4.BlockSize).ConstantTimeUnpad(plaintext)
	if err != nil {
		return "", fmt.Errorf("sm4 padding is invalid")
	}
	return string(unpadded), nil
}
