package fus

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testFUSConfig = &FUSConfig{
	SendEndpoint: "http://localhost", // overridden per test
	Salt:         "test-salt",
}

func newTestLogger(t *testing.T, server *httptest.Server) *Logger {
	t.Helper()
	cfg := testFUSConfig
	if server != nil {
		cfg = &FUSConfig{SendEndpoint: server.URL, Salt: "test-salt"}
	}
	logger, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TC",
			RecorderVersion: 1,
			ProductCode:     "TCC",
			BuildVersion:    "0.1.0",
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(cfg),
		WithClient(NewClient(cfg.SendEndpoint, 0, "")),
		WithValidator(newPermissiveValidator()),
	)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	return logger
}

func TestLoggerTrackAndFlush(t *testing.T) {
	var received Report
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	group := EventGroup{ID: "cli.command", Version: 1, State: false}
	logger.Track(group, "executed", map[string]any{"command": "run", "subcommand": "start"})
	logger.Track(group, "executed", map[string]any{"command": "auth", "subcommand": "login"})

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if len(received.Events) != 2 {
		t.Fatalf("received events = %d, want 2", len(received.Events))
	}

	e := received.Events[0]
	if e.Recorder.ID != "TC" {
		t.Errorf("recorder.id = %q, want TC", e.Recorder.ID)
	}
	if e.Product != "TCC" {
		t.Errorf("product = %q, want TCC", e.Product)
	}
	if e.Build != "0.1.0" {
		t.Errorf("build = %q, want 0.1.0", e.Build)
	}
	if !strings.HasSuffix(e.IDs["device"], "#C") {
		t.Errorf("device ID should be anonymized with #C suffix, got %q", e.IDs["device"])
	}
	if !strings.HasSuffix(e.Session, "#C") {
		t.Errorf("session should be anonymized with #C suffix, got %q", e.Session)
	}
	if e.Event.ID != "executed" {
		t.Errorf("event.id = %q, want executed", e.Event.ID)
	}
	if e.Event.Data["command"] != "run" {
		t.Errorf("event.data.command = %v, want run", e.Event.Data["command"])
	}
}

func TestLoggerFlushEmpty(t *testing.T) {
	logger := newTestLogger(t, nil)
	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush empty: %v", err)
	}
}

func TestLoggerReBuffersOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	logger.Track(EventGroup{ID: "test", Version: 1}, "test.event", nil)

	err := logger.Flush(t.Context())
	if err == nil {
		t.Fatal("expected error from failed flush")
	}

	// Events should be re-buffered.
	events, readErr := logger.buf.ReadAndClear()
	if readErr != nil {
		t.Fatalf("read buffer: %v", readErr)
	}
	if len(events) != 1 {
		t.Errorf("re-buffered events = %d, want 1", len(events))
	}
}

func TestLoggerMergesIdenticalEvents(t *testing.T) {
	var received Report
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	group := EventGroup{ID: "cli.command", Version: 1, State: false}
	// Track 3 identical events (same group, eventID, data).
	logger.Track(group, "executed", map[string]any{"command": "run"})
	logger.Track(group, "executed", map[string]any{"command": "run"})
	logger.Track(group, "executed", map[string]any{"command": "run"})
	// Track 1 different event.
	logger.Track(group, "executed", map[string]any{"command": "build"})

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if len(received.Events) != 2 {
		t.Fatalf("received events = %d, want 2 (3 merged + 1 different)", len(received.Events))
	}
	if received.Events[0].Event.Count != 3 {
		t.Errorf("merged event count = %d, want 3", received.Events[0].Event.Count)
	}
	if received.Events[1].Event.Count != 1 {
		t.Errorf("non-merged event count = %d, want 1", received.Events[1].Event.Count)
	}
}

func TestLoggerReBuffersOnlyUnsentEvents(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusNoContent) // first batch succeeds
		} else {
			w.WriteHeader(http.StatusBadRequest) // second batch fails
		}
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	// Track 600 events — will be split into batch of 500 (succeeds) + 100 (fails).
	group := EventGroup{ID: "test", Version: 1}
	for i := range 600 {
		logger.Track(group, "event", map[string]any{"i": i})
	}

	err := logger.Flush(t.Context())
	if err == nil {
		t.Fatal("expected error from partial flush failure")
	}

	// Only the 100 unsent events should be re-buffered, not all 600.
	events, readErr := logger.buf.ReadAndClear()
	if readErr != nil {
		t.Fatalf("read buffer: %v", readErr)
	}
	if len(events) != 100 {
		t.Errorf("re-buffered events = %d, want 100 (only unsent batch)", len(events))
	}
}

func TestLoggerFailClosedOnEmptySalt(t *testing.T) {
	_, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TCX",
			RecorderVersion: 1,
			ProductCode:     "TCX",
			BuildVersion:    "0.1.0",
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(&FUSConfig{SendEndpoint: "http://localhost", Salt: ""}),
		WithValidator(newPermissiveValidator()),
	)
	if err == nil {
		t.Fatal("NewLogger should fail when salt is empty")
	}
}

func TestLoggerRequiresValidator(t *testing.T) {
	_, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TCX",
			RecorderVersion: 1,
			ProductCode:     "TCX",
			BuildVersion:    "0.1.0",
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(testFUSConfig),
	)
	if err == nil {
		t.Fatal("NewLogger should fail when no validator is configured")
	}
	if !strings.Contains(err.Error(), "validator is required") {
		t.Errorf("error should mention missing validator, got %v", err)
	}
}

func TestLoggerDropsOversizedEvents(t *testing.T) {
	var received Report
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	oversized := map[string]any{}
	for i := range MaxDataFields + 1 {
		oversized[fmt.Sprintf("f%d", i)] = i
	}
	logger.Track(EventGroup{ID: "grp", Version: 1}, "evt", oversized)

	ok := map[string]any{"a": 1}
	logger.Track(EventGroup{ID: "grp", Version: 1}, "evt", ok)

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(received.Events) != 1 {
		t.Fatalf("received events = %d, want 1 (oversized dropped)", len(received.Events))
	}
}

func TestLoggerEscapesEventStrings(t *testing.T) {
	var received Report
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	logger := newTestLogger(t, server)

	logger.Track(
		EventGroup{ID: "grp with space", Version: 1},
		"action invoked",
		map[string]any{
			"user.email": "foo\tbar",
			"ok":         "hello world",
		},
	)

	if err := logger.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(received.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(received.Events))
	}
	e := received.Events[0]
	if e.Group.ID != "grp_with_space" {
		t.Errorf("group.id = %q, want grp_with_space", e.Group.ID)
	}
	if e.Event.ID != "action invoked" {
		t.Errorf("event.id = %q, want 'action invoked' (spaces kept in values)", e.Event.ID)
	}
	if _, ok := e.Event.Data["user_email"]; !ok {
		t.Errorf("data key should be escaped to user_email, got keys %v", e.Event.Data)
	}
	if v, _ := e.Event.Data["user_email"].(string); v != "foo bar" {
		t.Errorf("data[user_email] = %q, want %q", v, "foo bar")
	}
	if v, _ := e.Event.Data["ok"].(string); v != "hello world" {
		t.Errorf("data[ok] = %q, want %q", v, "hello world")
	}
}

func TestLoggerWithCustomDeviceID(t *testing.T) {
	logger, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TC",
			RecorderVersion: 1,
			ProductCode:     "QDJVM",
			BuildVersion:    "2024.3",
			DataDir:         t.TempDir(),
			DeviceID:        "custom-device-id",
		},
		WithFUSConfig(testFUSConfig),
		WithValidator(newPermissiveValidator()),
	)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	if logger.deviceID != "custom-device-id" {
		t.Errorf("deviceID = %q, want custom-device-id", logger.deviceID)
	}
}

func TestLoggerOnDropCallback(t *testing.T) {
	var drops []DropReason
	logger, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TC",
			RecorderVersion: 1,
			ProductCode:     "TCC",
			BuildVersion:    "0.1.0",
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(testFUSConfig),
		WithValidator(newPermissiveValidator()),
		WithOnDrop(func(group, event string, reason DropReason) {
			drops = append(drops, reason)
		}),
	)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	oversized := map[string]any{}
	for i := range MaxDataFields + 1 {
		oversized[fmt.Sprintf("f%d", i)] = i
	}
	logger.Track(EventGroup{ID: "grp", Version: 1}, "evt", oversized)

	if len(drops) != 1 || drops[0] != DropTooManyFields {
		t.Errorf("drops = %v, want [%s]", drops, DropTooManyFields)
	}
}

func TestGroupHelpers(t *testing.T) {
	g := Group("cli.command", 2)
	if g.ID != "cli.command" || g.Version != 2 || g.State != false {
		t.Errorf("Group() = %+v", g)
	}

	sg := StateGroup("cli.os", 3)
	if sg.ID != "cli.os" || sg.Version != 3 || sg.State != true {
		t.Errorf("StateGroup() = %+v", sg)
	}
}
