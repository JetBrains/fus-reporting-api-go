package fus_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JetBrains/fus-reporting-api-go"
)

// TestExamplePayload prints the exact LION v4 JSON that would be sent to the FUS endpoint.
// Run with: go test -v -run TestExamplePayload ./fus/
func TestExamplePayload(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(204)
	}))
	defer server.Close()

	// Real scheme: demonstrates how a product declares its groups/events/fields
	// in Go. The same Scheme value is both passed to NewValidator at runtime
	// and marshalled to schema.json for AP metadata registration.
	scheme := &fus.Scheme{
		Version: "1",
		Groups: []fus.GroupSchema{
			{
				ID: "cli.command",
				Rules: &fus.SchemeRules{
					EventID: []string{fus.EnumExpr("executed")},
					EventData: map[string][]string{
						"command":     {fus.EnumExpr("run")},
						"subcommand":  {fus.EnumExpr("start")},
						"duration_ms": {fus.RegexpExpr(`\d+`)},
					},
				},
			},
			{
				ID: "cli.auth",
				Rules: &fus.SchemeRules{
					EventID: []string{fus.EnumExpr("logged.in")},
					EventData: map[string][]string{
						"method": {fus.EnumExpr("pkce", "token")},
					},
				},
			},
			{
				ID: "qd.cl.system.os",
				Rules: &fus.SchemeRules{
					EventID: []string{fus.EnumExpr("os.name")},
					EventData: map[string][]string{
						"name": {fus.EnumExpr("darwin", "linux", "windows")},
						"arch": {fus.EnumExpr("amd64", "arm64")},
					},
				},
			},
		},
	}
	validator, err := fus.NewValidator(scheme)
	if err != nil {
		t.Fatal(err)
	}

	logger, err := fus.NewLogger(
		t.Context(),
		fus.RecorderConfig{
			RecorderID:      "FUS",
			RecorderVersion: 1,
			ProductCode:     "TCC",
			BuildVersion:    "0.1.0",
			DataDir:         t.TempDir(),
			DeviceID:        "test-device-id-12345",
		},
		fus.WithFUSConfig(&fus.FUSConfig{SendEndpoint: server.URL, Salt: "test-salt"}),
		fus.WithValidator(validator),
	)
	if err != nil {
		t.Fatal(err)
	}

	logger.Track(
		fus.EventGroup{ID: "cli.command", Version: 1, State: false},
		"executed",
		map[string]any{"command": "run", "subcommand": "start", "duration_ms": 1234},
	)
	logger.Track(
		fus.EventGroup{ID: "cli.auth", Version: 1, State: false},
		"logged.in",
		map[string]any{"method": "pkce"},
	)
	logger.Track(
		fus.EventGroup{ID: "qd.cl.system.os", Version: 1, State: true},
		"os.name",
		map[string]any{"name": "darwin", "arch": "arm64"},
	)

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	var pretty json.RawMessage
	if err := json.Unmarshal(captured, &pretty); err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))

	// Validate structure
	var report fus.Report
	if err := json.Unmarshal(captured, &report); err != nil {
		t.Fatalf("payload is not a valid Report: %v", err)
	}
	if len(report.Events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(report.Events))
	}
	for i, e := range report.Events {
		if e.Recorder.ID != "FUS" {
			t.Errorf("event[%d].recorder.id = %q, want FUS", i, e.Recorder.ID)
		}
		if e.Product != "TCC" {
			t.Errorf("event[%d].product = %q, want TCC", i, e.Product)
		}
		if e.IDs["device"] == "" {
			t.Errorf("event[%d].ids.device is empty", i)
		}
		if e.Session == "" {
			t.Errorf("event[%d].session is empty", i)
		}
		if e.Event.Count != 1 {
			t.Errorf("event[%d].event.count = %d, want 1", i, e.Event.Count)
		}
	}
}
