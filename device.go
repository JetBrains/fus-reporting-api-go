package fus

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const deviceIDFile = "fus_device_id"

// GetOrCreateDeviceID returns a persistent device ID from dataDir,
// generating one in ddMMyy+OSChar+UUID format if absent.
func GetOrCreateDeviceID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, deviceIDFile)

	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	id, err := generateDeviceID()
	if err != nil {
		return "", fmt.Errorf("generate device id: %w", err)
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("write device id: %w", err)
	}

	return id, nil
}

// generateDeviceID produces ddMMyy + OSChar + UUID (43 chars).
func generateDeviceID() (string, error) {
	now := time.Now()
	year := now.Year()
	if year < 2000 {
		year = 2000
	} else if year > 2099 {
		year = 2099
	}
	datePrefix := fmt.Sprintf("%02d%02d%02d", now.Day(), int(now.Month()), year%100)

	uuid, err := generateUUID()
	if err != nil {
		return "", err
	}

	return datePrefix + string(osChar()) + uuid, nil
}

func osChar() byte {
	switch runtime.GOOS {
	case "windows":
		return '1'
	case "darwin":
		return '2'
	case "linux":
		return '3'
	default:
		return '0'
	}
}

// Anonymize returns SHA-256(salt||value) as hex with a "#C" (client) suffix.
// Blank/whitespace values pass through unchanged.
func Anonymize(salt []byte, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil)) + "#C"
}

// ComputeBucket returns a value in [0, 256) derived from the device ID via SHA-256.
func ComputeBucket(deviceID string) int {
	h := sha256.Sum256([]byte(deviceID))
	return int(h[0]) % 256
}

func generateUUID() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
}
