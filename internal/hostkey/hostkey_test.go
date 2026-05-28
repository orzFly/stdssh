package hostkey

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFromSeedDeterministic(t *testing.T) {
	a, err := FromSeed("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromSeed("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.PublicKey().Marshal(), b.PublicKey().Marshal()) {
		t.Fatal("same seed produced different public keys")
	}

	c, err := FromSeed("different")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.PublicKey().Marshal(), c.PublicKey().Marshal()) {
		t.Fatal("different seeds produced same public key")
	}
}

func TestFromSeedEmpty(t *testing.T) {
	if _, err := FromSeed(""); err == nil {
		t.Fatal("expected error on empty seed")
	}
}

func TestLoadOrCreateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hostkey")

	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}

	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(first.PublicKey().Marshal(), second.PublicKey().Marshal()) {
		t.Fatal("reload produced different public key")
	}
}
