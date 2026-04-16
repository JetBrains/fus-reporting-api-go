package fus

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// cdnMetadataJSON mirrors the JVM EventGroupRemoteDescriptors wire format:
// braced rule expressions, extra top-level fields (client_data, system_data,
// ids) and per-group anonymized_fields — all of which the Go Scheme ignores.
const cdnMetadataJSON = `{
	"version": "123",
	"rules": {
		"enums": {"boolean": ["true","false"]},
		"regexps": {"integer": "-?\\d+"}
	},
	"groups": [{
		"id": "actions",
		"builds": [],
		"versions": [{"from": "1", "to": "2"}],
		"rules": {
			"event_id": ["{enum:click|press}"],
			"event_data": {"enabled": ["{enum#boolean}"]}
		},
		"anonymized_fields": [{"event": "click", "fields": ["user_id"]}]
	}],
	"client_data": [{"path": "os", "revisions": []}],
	"system_data": null,
	"ids": null
}`

func serveCDNMetadata(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cdnMetadataJSON))
	}))
}

func TestFetchScheme_CDNFormat(t *testing.T) {
	srv := serveCDNMetadata(t)
	defer srv.Close()

	got, err := FetchScheme(srv.URL)
	if err != nil {
		t.Fatalf("FetchScheme: %v", err)
	}
	if got.Version != "123" {
		t.Errorf("version = %q, want 123", got.Version)
	}
	if len(got.Groups) != 1 || got.Groups[0].ID != "actions" {
		t.Fatalf("groups = %v, want [actions]", got.Groups)
	}
	// Braced expressions must compile into a usable validator.
	if _, err := NewValidator(got); err != nil {
		t.Errorf("NewValidator on CDN scheme: %v", err)
	}
}

func TestFetchScheme_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := FetchScheme(srv.URL); err == nil {
		t.Fatal("expected error for 403")
	}
}

func seedCache(t *testing.T, dir string, scheme *Scheme, age time.Duration) {
	t.Helper()
	data, _ := json.Marshal(scheme)
	os.WriteFile(filepath.Join(dir, metadataCacheFile), data, 0o600)
	ts := time.Now().Add(-age).Unix()
	os.WriteFile(filepath.Join(dir, metadataCacheFile+metadataCacheMetaExt),
		[]byte(strconv.FormatInt(ts, 10)), 0o600)
}

func TestLoadOrFetchScheme_FreshCache(t *testing.T) {
	dir := t.TempDir()
	seedCache(t, dir, &Scheme{Version: "1", Groups: []GroupSchema{{ID: "g"}}}, 0)

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	got, err := LoadOrFetchScheme(srv.URL, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("server was called despite fresh cache")
	}
	if got.Version != "1" {
		t.Errorf("version = %q, want 1", got.Version)
	}
}

func TestLoadOrFetchScheme_StaleCache_RefreshesFromRemote(t *testing.T) {
	dir := t.TempDir()
	seedCache(t, dir, &Scheme{Version: "old", Groups: []GroupSchema{{ID: "g"}}}, 2*metadataCacheTTL)

	srv := serveCDNMetadata(t)
	defer srv.Close()

	got, err := LoadOrFetchScheme(srv.URL, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != "123" {
		t.Errorf("version = %q, want 123 (from remote)", got.Version)
	}
}

func TestLoadOrFetchScheme_StaleCache_FallsBackOnError(t *testing.T) {
	dir := t.TempDir()
	seedCache(t, dir, &Scheme{Version: "cached", Groups: []GroupSchema{{ID: "g"}}}, 2*metadataCacheTTL)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	got, err := LoadOrFetchScheme(srv.URL, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != "cached" {
		t.Errorf("version = %q, want cached (fallback)", got.Version)
	}
}

func TestLoadOrFetchScheme_NoCacheAndFetchFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := LoadOrFetchScheme(srv.URL, t.TempDir()); err == nil {
		t.Fatal("expected error when both cache and fetch fail")
	}
}

func TestLoadOrFetchScheme_EmptyURL(t *testing.T) {
	if _, err := LoadOrFetchScheme("", t.TempDir()); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestFUSConfig_SchemeURL(t *testing.T) {
	for _, tt := range []struct {
		endpoint, product, want string
	}{
		{"https://cdn.example.com/metadata", "TCX", "https://cdn.example.com/metadata/TCX.json"},
		{"https://cdn.example.com/metadata/", "TCX", "https://cdn.example.com/metadata/TCX.json"},
		{"", "TCX", ""},
	} {
		cfg := &FUSConfig{MetadataEndpoint: tt.endpoint}
		if got := cfg.SchemeURL(tt.product); got != tt.want {
			t.Errorf("SchemeURL(%q, %q) = %q, want %q", tt.endpoint, tt.product, got, tt.want)
		}
	}
}
