package runtime

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// AESGCMCodec stores a versioned nonce+ciphertext envelope. The key must come
// from a KMS/Vault reference or a process secret, never from channel payloads.
type AESGCMCodec struct {
	key     []byte
	version string
}

func (c AESGCMCodec) Version() string { return c.version }

func NewAESGCMCodec(key []byte, version string) (AESGCMCodec, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return AESGCMCodec{}, fmt.Errorf("payload key must be 16, 24, or 32 bytes")
	}
	if version == "" {
		version = "v1"
	}
	return AESGCMCodec{key: append([]byte(nil), key...), version: version}, nil
}

func (c AESGCMCodec) Encode(value any) ([]byte, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plain, []byte(c.version))
	envelope := struct {
		Version    string `json:"v"`
		Nonce      string `json:"n"`
		Ciphertext string `json:"c"`
	}{c.version, base64.RawURLEncoding.EncodeToString(nonce), base64.RawURLEncoding.EncodeToString(sealed)}
	return json.Marshal(envelope)
}

func (c AESGCMCodec) Decode(data []byte, value any) error {
	var envelope struct {
		Version    string `json:"v"`
		Nonce      string `json:"n"`
		Ciphertext string `json:"c"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Version != c.version {
		return fmt.Errorf("payload version mismatch")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(c.version))
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, value)
}
