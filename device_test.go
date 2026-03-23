package fus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnonymize(t *testing.T) {
	salt := []byte("test-salt")
	result := Anonymize(salt, "my-device-id")

	if !strings.HasSuffix(result, "#C") {
		t.Errorf("anonymized value should end with #C, got %q", result)
	}

	hex := strings.TrimSuffix(result, "#C")
	if len(hex) != 64 {
		t.Errorf("SHA-256 hex should be 64 chars, got %d", len(hex))
	}

	// Same input produces same output.
	result2 := Anonymize(salt, "my-device-id")
	if result != result2 {
		t.Errorf("anonymize not deterministic: %q != %q", result, result2)
	}

	// Different salt produces different output.
	result3 := Anonymize([]byte("other-salt"), "my-device-id")
	if result == result3 {
		t.Error("different salts should produce different hashes")
	}
}

func TestAnonymizeBlank(t *testing.T) {
	salt := []byte("test-salt")
	if got := Anonymize(salt, ""); got != "" {
		t.Errorf("blank input should return blank, got %q", got)
	}
	if got := Anonymize(salt, "   "); got != "   " {
		t.Errorf("whitespace input should return whitespace, got %q", got)
	}
}

func TestComputeBucket(t *testing.T) {
	bucket := ComputeBucket("test-device-id")
	if bucket < 0 || bucket >= 256 {
		t.Errorf("bucket out of range [0, 256): %d", bucket)
	}

	// Deterministic.
	bucket2 := ComputeBucket("test-device-id")
	if bucket != bucket2 {
		t.Errorf("bucket not deterministic: %d != %d", bucket, bucket2)
	}
}

func TestGetOrCreateDeviceID(t *testing.T) {
	dir := t.TempDir()

	id1, err := GetOrCreateDeviceID(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if id1 == "" {
		t.Fatal("device ID should not be empty")
	}

	// Format: ddMMyy + OSChar + UUID = 7 prefix chars + 36 UUID chars = 43 total.
	if len(id1) != 43 {
		t.Errorf("device ID length = %d, want 43: %q", len(id1), id1)
	}

	// First 6 chars are date digits, 7th is OS char.
	prefix := id1[:6]
	for _, c := range prefix {
		if c < '0' || c > '9' {
			t.Errorf("date prefix should be digits, got %q in %q", string(c), id1)
		}
	}
	osChar := id1[6]
	if osChar != '0' && osChar != '1' && osChar != '2' && osChar != '3' {
		t.Errorf("OS char should be 0-3, got %q", string(osChar))
	}

	// Remainder is UUID v4 format.
	uuidPart := id1[7:]
	parts := strings.Split(uuidPart, "-")
	if len(parts) != 5 {
		t.Errorf("UUID portion should have 5 parts, got %d: %q", len(parts), uuidPart)
	}

	// Second call returns the same ID.
	id2, err := GetOrCreateDeviceID(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("device ID changed: %q != %q", id1, id2)
	}

	// File exists with correct permissions.
	path := filepath.Join(dir, deviceIDFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestGetOrCreateDeviceIDCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")

	id, err := GetOrCreateDeviceID(dir)
	if err != nil {
		t.Fatalf("create with nested dir: %v", err)
	}
	if id == "" {
		t.Fatal("device ID should not be empty")
	}
}
