package fus

import (
	"testing"
)

// sink prevents the compiler from eliminating benchmark work as dead code.
var sink any

var benchScheme = &Scheme{
	Version: "1",
	Rules: &SchemeRules{
		Enums:   map[string][]string{"boolean": {"true", "false"}},
		Regexps: map[string]string{"version": `\d+\.\d+\.\d+`},
		Ranges:  map[string]IntRange{"uint8": {From: 0, To: 255}},
	},
	Groups: []GroupSchema{
		{
			ID:     "cli.session",
			Builds: []SchemeRange{{From: "0.1.0"}},
			Rules: &SchemeRules{
				EventID: []string{"enum:started|stopped"},
				EventData: map[string][]string{
					"os":          {"enum:darwin|linux|windows"},
					"cli_version": {"regexp#version"},
					"has_linked":  {"enum#boolean"},
				},
			},
		},
		{
			ID: "cli.command",
			Rules: &SchemeRules{
				EventID: []string{"enum:executed"},
				EventData: map[string][]string{
					"exit_code": {"range:0..2"},
					"bucket":    {"range#uint8"},
				},
			},
		},
	},
}

func benchValidator(b *testing.B) *Validator {
	b.Helper()
	v, err := NewValidator(benchScheme)
	if err != nil {
		b.Fatal(err)
	}
	return v
}

func BenchmarkValidate(b *testing.B) {
	v := benchValidator(b)
	event := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "cli.session", Version: 1},
		Event: EventAction{
			ID:    "started",
			Data:  map[string]any{"os": "darwin", "cli_version": "1.2.3", "has_linked": true},
			Count: 1,
		},
	}

	for b.Loop() {
		sink, _ = v.Validate(event)
	}
}

func BenchmarkValidate_Reject(b *testing.B) {
	v := benchValidator(b)
	event := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "cli.session", Version: 1},
		Event: EventAction{
			ID:   "started",
			Data: map[string]any{"os": "plan9", "cli_version": "bad", "secret": "leak"},
		},
	}

	for b.Loop() {
		sink, _ = v.Validate(event)
	}
}

func BenchmarkAnonymize(b *testing.B) {
	salt := []byte("benchmark-salt-value")
	for b.Loop() {
		sink = Anonymize(salt, "some-device-id-value-12345")
	}
}

func BenchmarkAnonymizeEvent(b *testing.B) {
	a := NewAnonymizer(&Scheme{
		Groups: []GroupSchema{{
			ID:               "actions",
			AnonymizedFields: []AnonymizedField{{Event: "click", Fields: []string{"user_id", "project_id"}}},
		}},
	}, []byte("bench-salt"))

	for b.Loop() {
		ev := LogEvent{
			Group: EventGroup{ID: "actions", Version: 1},
			Event: EventAction{
				ID:   "click",
				Data: map[string]any{"user_id": "/user/123", "project_id": "proj-456"},
			},
		}
		a.AnonymizeEvent(&ev)
		sink = ev
	}
}

func BenchmarkEscapeEvent(b *testing.B) {
	b.Run("dirty", func(b *testing.B) {
		event := LogEvent{
			Recorder: Recorder{ID: "TC", Version: 1},
			Product:  "TCC",
			IDs:      map[string]string{"device": "hash#C", "server.id": "tc prod"},
			Build:    "0.1.0",
			Group:    EventGroup{ID: "cli.command", Version: 1},
			Event: EventAction{
				ID:   "action invoked",
				Data: map[string]any{"user.email": "foo\tbar", "ok": "hello world"},
			},
		}
		for b.Loop() {
			sink = escapeLogEvent(event)
		}
	})

	b.Run("clean", func(b *testing.B) {
		event := LogEvent{
			Recorder: Recorder{ID: "TC", Version: 1},
			Product:  "TCC",
			IDs:      map[string]string{"device": "hash"},
			Build:    "0.1.0",
			Group:    EventGroup{ID: "cli_command", Version: 1},
			Event: EventAction{
				ID:   "executed",
				Data: map[string]any{"command": "run", "exit_code": 0},
			},
		}
		for b.Loop() {
			sink = escapeLogEvent(event)
		}
	})
}

func BenchmarkMergeEvents(b *testing.B) {
	base := LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "d"},
		Group:    EventGroup{ID: "g", Version: 1},
		Event:    EventAction{ID: "e", Data: map[string]any{"k": "v"}, Count: 1},
	}

	b.Run("all_same", func(b *testing.B) {
		events := make([]LogEvent, 100)
		for i := range events {
			events[i] = base
		}
		for b.Loop() {
			sink = mergeEvents(events)
		}
	})

	b.Run("all_different", func(b *testing.B) {
		events := make([]LogEvent, 100)
		for i := range events {
			events[i] = base
			events[i].Event.ID = string(rune('a' + i%26))
		}
		for b.Loop() {
			sink = mergeEvents(events)
		}
	})

	b.Run("mixed", func(b *testing.B) {
		events := make([]LogEvent, 100)
		for i := range events {
			events[i] = base
			if i%10 == 0 {
				events[i].Event.ID = "different"
			}
		}
		for b.Loop() {
			sink = mergeEvents(events)
		}
	})
}

func BenchmarkComputeBucket(b *testing.B) {
	for b.Loop() {
		sink = ComputeBucket("160426200fedcba0-1234-4567-89ab-0123456789ab")
	}
}

func BenchmarkBufferAppend(b *testing.B) {
	buf := newBuffer(b.TempDir(), 10000)
	event := LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "hash#C"},
		Group:    EventGroup{ID: "cli.command", Version: 1},
		Event:    EventAction{ID: "executed", Data: map[string]any{"command": "run"}, Count: 1},
	}

	for b.Loop() {
		_ = buf.Append(event)
	}
}

func BenchmarkBufferReadAndClear(b *testing.B) {
	dir := b.TempDir()
	buf := newBuffer(dir, 10000)

	event := LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "hash#C"},
		Group:    EventGroup{ID: "cli.command", Version: 1},
		Event:    EventAction{ID: "executed", Data: map[string]any{"command": "run"}, Count: 1},
	}

	// Seed 100 events before each iteration.
	for b.Loop() {
		b.StopTimer()
		for range 100 {
			_ = buf.Append(event)
		}
		b.StartTimer()
		sink, _ = buf.ReadAndClear()
	}
}

func BenchmarkTrack(b *testing.B) {
	validator := benchValidator(b)
	anonymizer := NewAnonymizer(benchScheme, []byte("bench-salt"))

	logger, err := NewLogger(
		b.Context(),
		RecorderConfig{
			RecorderID:      "TC",
			RecorderVersion: 1,
			ProductCode:     "TCC",
			BuildVersion:    "0.2.0",
			DataDir:         b.TempDir(),
			DeviceID:        "bench-device",
		},
		WithFUSConfig(&FUSConfig{SendEndpoint: "http://localhost", Salt: "bench-salt"}),
		WithValidator(validator),
		WithAnonymizer(anonymizer),
	)
	if err != nil {
		b.Fatal(err)
	}

	group := EventGroup{ID: "cli.session", Version: 1}
	data := map[string]any{"os": "darwin", "cli_version": "1.2.3", "has_linked": true}

	for b.Loop() {
		logger.Track(group, "started", data)
	}
}
