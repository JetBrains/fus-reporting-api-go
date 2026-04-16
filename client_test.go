package fus

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSend(t *testing.T) {
	var received Report
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, "")

	report := Report{
		Events: []LogEvent{
			{
				Recorder: Recorder{ID: "TC", Version: 1},
				Product:  "TCC",
				IDs:      map[string]string{"device": "hash#C"},
				Time:     1700000000000,
				Build:    "0.1.0",
				Session:  "sess#C",
				Group:    EventGroup{ID: "cli.command", Version: 1},
				Bucket:   42,
				Event:    EventAction{ID: "executed", Data: map[string]any{"command": "run"}, Count: 1},
			},
		},
	}

	ctx := t.Context()
	if err := client.Send(ctx, report); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(received.Events) != 1 {
		t.Fatalf("received events = %d, want 1", len(received.Events))
	}
	if received.Events[0].Event.ID != "executed" {
		t.Errorf("received event.id = %q, want executed", received.Events[0].Event.ID)
	}
}

func TestClientSendEmpty(t *testing.T) {
	client := NewClient("http://should-not-be-called", 1*time.Second, "")
	if err := client.Send(t.Context(), Report{}); err != nil {
		t.Fatalf("send empty: %v", err)
	}
}

func TestClientSendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, "")
	err := client.Send(t.Context(), Report{Events: []LogEvent{{}}})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestClientSendRetriesTransientErrors(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, "")
	err := client.Send(t.Context(), Report{Events: []LogEvent{{}}})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestClientSendBatched(t *testing.T) {
	var batchCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batchCount++
		body, _ := io.ReadAll(r.Body)
		var report Report
		_ = json.Unmarshal(body, &report)
		if len(report.Events) > maxBatchSize {
			t.Errorf("batch size = %d, exceeds max %d", len(report.Events), maxBatchSize)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, "")

	events := make([]LogEvent, 1200)
	for i := range events {
		events[i] = LogEvent{
			Recorder: Recorder{ID: "TC", Version: 1},
			Event:    EventAction{ID: "test", Count: 1},
		}
	}

	sent, err := client.SendBatched(t.Context(), events)
	if err != nil {
		t.Fatalf("send batched: %v", err)
	}
	if sent != 1200 {
		t.Errorf("sent = %d, want 1200", sent)
	}
	if batchCount != 3 {
		t.Errorf("batch count = %d, want 3", batchCount)
	}
}
