package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fernet/fernet-go"
)

type Encryptor struct {
	key *fernet.Key
}

func NewEncryptor(keyPath string) (*Encryptor, error) {
	keyBytes, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	key, err := fernet.DecodeKey(strings.TrimSpace(string(keyBytes)))
	if err != nil {
		return nil, fmt.Errorf("decode fernet key: %w", err)
	}
	return &Encryptor{key: key}, nil
}

func (e *Encryptor) Encrypt(value string) ([]byte, error) {
	token, err := fernet.EncryptAndSign([]byte(value), e.key)
	if err != nil {
		return nil, fmt.Errorf("encrypt value: %w", err)
	}
	return token, nil
}

func (e *Encryptor) Decrypt(value []byte) (string, error) {
	plain := fernet.VerifyAndDecrypt(value, 0, []*fernet.Key{e.key})
	if plain == nil {
		return "", fmt.Errorf("decrypt value: invalid token")
	}
	return string(plain), nil
}

func loadOrCreateKey(keyPath string) ([]byte, error) {
	if data, err := os.ReadFile(keyPath); err == nil {
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read encryption key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create encryption key directory: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate encryption key: %w", err)
	}
	encoded := base64.URLEncoding.EncodeToString(raw)
	if err := os.WriteFile(keyPath, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("write encryption key: %w", err)
	}
	return []byte(encoded), nil
}
