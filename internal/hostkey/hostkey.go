// Package hostkey loads or derives the server's SSH host key.
package hostkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/ssh"
)

const seedInfo = "stdssh hostkey ed25519 v1"

// FromSeed derives a deterministic ed25519 host key from seed via HKDF-SHA256.
func FromSeed(seed string) (ssh.Signer, error) {
	if seed == "" {
		return nil, errors.New("hostkey: empty seed")
	}
	r := hkdf.New(sha256.New, []byte(seed), nil, []byte(seedInfo))
	out := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("hostkey: hkdf: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(out)
	return ssh.NewSignerFromKey(priv)
}

// LoadOrCreate reads a PEM-encoded private key from path, or generates a new
// ed25519 key (writing it with mode 0600, creating parents with 0700) if the
// file is missing.
func LoadOrCreate(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("hostkey: empty path")
	}
	buf, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, err := ssh.ParseRawPrivateKey(buf)
		if err != nil {
			return nil, fmt.Errorf("hostkey: parse %s: %w", path, err)
		}
		return ssh.NewSignerFromKey(key)
	case errors.Is(err, os.ErrNotExist):
		return generateAndWrite(path)
	default:
		return nil, fmt.Errorf("hostkey: read %s: %w", path, err)
	}
}

func generateAndWrite(path string) (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("hostkey: generate: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "stdssh")
	if err != nil {
		return nil, fmt.Errorf("hostkey: marshal: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("hostkey: mkdir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("hostkey: write %s: %w", path, err)
	}
	return ssh.NewSignerFromKey(priv)
}
