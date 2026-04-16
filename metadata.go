package fus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Mirrors JVM MetadataFileUpdater constants.
const (
	metadataCacheFile    = "events-scheme.json"
	metadataCacheMetaExt = ".meta" // stores last-modified epoch
	metadataCacheTTL     = 1 * time.Hour
	metadataFetchTimeout = 5 * time.Second
)

// FetchScheme downloads event group metadata from url and decodes it into a
// Scheme. The response format matches the JVM EventGroupRemoteDescriptors JSON
// published to the AP metadata CDN.
func FetchScheme(url string) (*Scheme, error) {
	client := &http.Client{Timeout: metadataFetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata: status %d", resp.StatusCode)
	}

	var s Scheme
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	return &s, nil
}

// LoadOrFetchScheme returns the event group metadata for schemeURL, using a
// disk cache in dataDir with TTL-based freshness. When the remote is
// unreachable it falls back to the cached copy. This mirrors the JVM
// MetadataFileUpdater.getNewestFile flow.
//
// schemeURL is the full metadata URL (use FUSConfig.SchemeURL to build it).
// Returns an error only when both remote and cache are unavailable.
func LoadOrFetchScheme(schemeURL, dataDir string) (*Scheme, error) {
	if schemeURL == "" {
		return nil, fmt.Errorf("fus: empty metadata URL")
	}

	cachePath := filepath.Join(dataDir, metadataCacheFile)
	metaPath := cachePath + metadataCacheMetaExt

	cached, cacheErr := LoadSchemeFromFile(cachePath)
	cacheUsable := cacheErr == nil && len(cached.Groups) > 0

	if cacheUsable && isCacheFresh(metaPath) {
		return cached, nil
	}

	scheme, err := FetchScheme(schemeURL)
	if err == nil {
		writeSchemeCache(cachePath, metaPath, scheme)
		return scheme, nil
	}

	if cacheUsable {
		return cached, nil
	}

	return nil, fmt.Errorf("fus: no usable metadata (fetch failed: %w)", err)
}

func isCacheFresh(metaPath string) bool {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < metadataCacheTTL
}

func writeSchemeCache(cachePath, metaPath string, s *Scheme) {
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(cachePath, data, 0o600)
	_ = os.WriteFile(metaPath, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
}
