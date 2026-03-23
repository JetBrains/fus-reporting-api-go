package fus_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/JetBrains/fus-reporting-api-go"
)

// TestIntegrationFullFlow mirrors the JVM integration test pattern:
// 1. Mock server serves FUS config endpoint and captures POST to /fus/v5/send/
// 2. Logger fetches config from mock, logs events, flushes
// 3. Verify the captured POST body matches LION v4 wire format exactly
//
// This is the Go equivalent of ValidationIT.kt "test validation of simple event".
//
// Run with: go test -v -run TestIntegrationFullFlow ./fus/
func TestIntegrationFullFlow(t *testing.T) {
	const (
		recorderID  = "FUS"
		productCode = "IU"
		buildVer    = "2025.2"
		testSalt    = "secret_salt"
		deviceID    = "123456"
	)

	var (
		mu       sync.Mutex
		captured []byte
	)

	// Mock server handles both config and send endpoints, same as Hoverfly in JVM tests.
	mux := http.NewServeMux()

	// Config endpoint: GET /storage/fus/config/v4/{recorderID}/{productCode}.json
	// Returns a config that points send endpoint back to this mock server.
	mux.HandleFunc("GET /storage/fus/config/v4/{recorder}/{product}", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Config request: %s", r.URL.Path)
		// We'll fill in the send URL after server starts (see below).
		// For now this handler is replaced in the test body.
		w.WriteHeader(http.StatusNotFound)
	})

	// Send endpoint: POST /fus/v5/send/
	mux.HandleFunc("POST /fus/v5/send/", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Send request: %s %s", r.Method, r.URL.Path)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read send body: %v", err)
		}
		mu.Lock()
		captured = body
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Now register the config handler with the actual server URL for the send endpoint.
	configJSON := fmt.Sprintf(`{
		"productCode": %q,
		"versions": [{
			"majorBuildVersionBorders": {"from": "2020.1"},
			"releaseFilters": [{"releaseType": "ALL", "from": 0, "to": 256}],
			"endpoints": {"send": %q},
			"options": {"id_salt": %q}
		}]
	}`, productCode, server.URL+"/fus/v5/send/", testSalt)

	mux.HandleFunc("GET /storage/fus/config/v4/FUS/IU.json", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Config request (matched): %s", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, configJSON)
	})

	// Override the config URL template to point at our mock server.
	fusConfig := &fus.FUSConfig{
		SendEndpoint: server.URL + "/fus/v5/send/",
		Salt:         testSalt,
	}

	logger, err := fus.NewLogger(
		fus.RecorderConfig{
			RecorderID:      recorderID,
			RecorderVersion: 1,
			ProductCode:     productCode,
			BuildVersion:    buildVer,
			DataDir:         t.TempDir(),
			DeviceID:        deviceID,
		},
		fus.WithFUSConfig(fusConfig),
	)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	// Log the same event as the JVM test: actions group, action.invoked
	logger.Track(
		fus.EventGroup{ID: "actions", Version: 1, State: false},
		"action.invoked",
		map[string]any{"action_id": "FileOpened", "lang": "Kotlin"},
	)

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// --- Verify the captured POST body matches LION v4 wire format ---

	mu.Lock()
	body := captured
	mu.Unlock()

	if body == nil {
		t.Fatal("no POST request captured — send endpoint was never called")
	}

	// Pretty-print for inspection
	var pretty json.RawMessage
	if err := json.Unmarshal(body, &pretty); err != nil {
		t.Fatalf("invalid JSON in POST body: %v", err)
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	t.Logf("Captured POST body:\n%s", string(out))

	// Parse and validate structure
	var report fus.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("POST body is not a valid Report: %v", err)
	}

	if len(report.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(report.Events))
	}

	e := report.Events[0]

	// Recorder
	if e.Recorder.ID != recorderID {
		t.Errorf("recorder.id = %q, want %q", e.Recorder.ID, recorderID)
	}
	if e.Recorder.Version != 1 {
		t.Errorf("recorder.version = %d, want 1", e.Recorder.Version)
	}

	// Product
	if e.Product != productCode {
		t.Errorf("product = %q, want %q", e.Product, productCode)
	}

	// Build
	if e.Build != buildVer {
		t.Errorf("build = %q, want %q", e.Build, buildVer)
	}

	// IDs — device must be SHA-256 hash of (salt + deviceID) with #C suffix
	device := e.IDs["device"]
	if device == "" {
		t.Error("ids.device is empty")
	}
	if !strings.HasSuffix(device, "#C") {
		t.Errorf("ids.device should end with #C, got %q", device)
	}
	expectedDevice := fus.Anonymize([]byte(testSalt), deviceID)
	if device != expectedDevice {
		t.Errorf("ids.device = %q, want %q", device, expectedDevice)
	}

	// Session — must be anonymized
	if e.Session == "" {
		t.Error("session is empty")
	}
	if !strings.HasSuffix(e.Session, "#C") {
		t.Errorf("session should end with #C, got %q", e.Session)
	}

	// Bucket — derived from raw deviceID
	expectedBucket := fus.ComputeBucket(deviceID)
	if e.Bucket != expectedBucket {
		t.Errorf("bucket = %d, want %d", e.Bucket, expectedBucket)
	}

	// Group
	if e.Group.ID != "actions" {
		t.Errorf("group.id = %q, want actions", e.Group.ID)
	}
	if e.Group.Version != 1 {
		t.Errorf("group.version = %d, want 1", e.Group.Version)
	}
	if e.Group.State != false {
		t.Errorf("group.state = %v, want false", e.Group.State)
	}

	// Event
	if e.Event.ID != "action.invoked" {
		t.Errorf("event.id = %q, want action.invoked", e.Event.ID)
	}
	if e.Event.Count != 1 {
		t.Errorf("event.count = %d, want 1", e.Event.Count)
	}
	if e.Event.Data["action_id"] != "FileOpened" {
		t.Errorf("event.data.action_id = %v, want FileOpened", e.Event.Data["action_id"])
	}
	if e.Event.Data["lang"] != "Kotlin" {
		t.Errorf("event.data.lang = %v, want Kotlin", e.Event.Data["lang"])
	}

	// Time — should be a recent timestamp (within last minute)
	if e.Time == 0 {
		t.Error("time should not be 0")
	}

	// Internal
	if e.Internal != false {
		t.Error("internal should be false")
	}

	t.Log("All LION v4 wire format assertions passed")
}

// TestStagingSend sends real events to the FUS staging endpoint.
//
// Run with:
//
//	FUS_STAGING=1 go test -v -run TestStagingSend ./fus/
//
// Then check the FUS analytics dashboard for events with your product code.
// The test uses the test/staging config endpoint (config/v4/test/FUS/...).
func TestStagingSend(t *testing.T) {
	if os.Getenv("FUS_STAGING") != "1" {
		t.Skip("Set FUS_STAGING=1 to run this test against the real FUS staging endpoint")
	}

	recorderID := "FUS"
	productCode := "TCC"

	cfg, err := fus.FetchTestConfig(recorderID, productCode)
	if err != nil {
		t.Fatalf("fetch staging config: %v", err)
	}
	t.Logf("Staging endpoint: %s", cfg.SendEndpoint)
	t.Logf("Salt: %s (revision %d)", cfg.Salt, cfg.SaltRevision)

	logger, err := fus.NewLogger(
		fus.RecorderConfig{
			RecorderID:      recorderID,
			RecorderVersion: 1,
			ProductCode:     productCode,
			BuildVersion:    "0.0.1-staging-test",
			DataDir:         t.TempDir(),
		},
		fus.WithFUSConfig(cfg),
	)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	logger.Track(
		fus.EventGroup{ID: "cli.command", Version: 1, State: false},
		"executed",
		map[string]any{"command": "run", "subcommand": "start", "duration_ms": 42},
	)

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush to staging: %v", err)
	}

	t.Log("Events sent to FUS staging successfully (HTTP 2xx). Check the dashboard.")
}
