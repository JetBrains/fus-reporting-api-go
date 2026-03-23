package fus

import (
	"encoding/json"
	"testing"
)

func TestLogEventWireFormat(t *testing.T) {
	event := LogEvent{
		Recorder: Recorder{ID: "FUS", Version: 1},
		Product:  "IU",
		IDs:      map[string]string{"device": "51e6daf1e987f630dc7264aa0f394929753f821665b916bc24da1f075b808a1e#C"},
		Internal: false,
		Time:     0,
		Build:    "2025.2",
		Session:  "e4c2df3bc83aac088053eec50a1513ff25a5b260ce31d1c95e072e2144f2e1f1#C",
		Group:    EventGroup{ID: "actions", Version: 1, State: false},
		Bucket:   99,
		Event: EventAction{
			ID:    "action.invoked",
			Data:  map[string]any{"lang": "Kotlin", "action_id": "FileOpened"},
			Count: 1,
		},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	recorder := parsed["recorder"].(map[string]any)
	if recorder["id"] != "FUS" {
		t.Errorf("recorder.id = %v, want FUS", recorder["id"])
	}
	if recorder["version"].(float64) != 1 {
		t.Errorf("recorder.version = %v, want 1", recorder["version"])
	}

	group := parsed["group"].(map[string]any)
	if group["id"] != "actions" {
		t.Errorf("group.id = %v, want actions", group["id"])
	}
	if group["state"] != false {
		t.Errorf("group.state = %v, want false", group["state"])
	}

	ev := parsed["event"].(map[string]any)
	if ev["id"] != "action.invoked" {
		t.Errorf("event.id = %v, want action.invoked", ev["id"])
	}
	if ev["count"].(float64) != 1 {
		t.Errorf("event.count = %v, want 1", ev["count"])
	}

	evData := ev["data"].(map[string]any)
	if evData["lang"] != "Kotlin" {
		t.Errorf("event.data.lang = %v, want Kotlin", evData["lang"])
	}
}

func TestReportWireFormat(t *testing.T) {
	report := Report{
		Events: []LogEvent{
			{
				Recorder: Recorder{ID: "TC", Version: 1},
				Product:  "TCC",
				IDs:      map[string]string{"device": "abc123#C"},
				Time:     1700000000000,
				Build:    "0.1.0",
				Session:  "sess#C",
				Group:    EventGroup{ID: "cli.command", Version: 1, State: false},
				Bucket:   42,
				Event:    EventAction{ID: "executed", Count: 1},
			},
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	events := parsed["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
}

func TestLogEventOmitsEmptyOptionalFields(t *testing.T) {
	event := LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "x#C"},
		Group:    EventGroup{ID: "test", Version: 1},
		Event:    EventAction{ID: "test", Count: 1},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := parsed["system_data"]; ok {
		t.Error("system_data should be omitted when nil")
	}
	if _, ok := parsed["client_data"]; ok {
		t.Error("client_data should be omitted when nil")
	}
}

func TestRoundTrip(t *testing.T) {
	original := LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "hash#C"},
		Internal: true,
		Time:     1700000000000,
		Build:    "1.0.0",
		Session:  "sess#C",
		Group:    EventGroup{ID: "cli.auth", Version: 2, State: false},
		Bucket:   128,
		Event: EventAction{
			ID:    "logged.in",
			Data:  map[string]any{"method": "pkce"},
			Count: 1,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded LogEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Recorder.ID != original.Recorder.ID {
		t.Errorf("recorder.id = %v, want %v", decoded.Recorder.ID, original.Recorder.ID)
	}
	if decoded.Event.ID != original.Event.ID {
		t.Errorf("event.id = %v, want %v", decoded.Event.ID, original.Event.ID)
	}
	if decoded.Event.Data["method"] != original.Event.Data["method"] {
		t.Errorf("event.data.method = %v, want %v", decoded.Event.Data["method"], original.Event.Data["method"])
	}
}
