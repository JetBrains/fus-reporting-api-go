package fus

import (
	"reflect"
	"testing"
)

func TestLoggerAnonymizesBeforeValidation(t *testing.T) {
	validator, err := NewValidator(&Scheme{
		Rules: &SchemeRules{Regexps: map[string]string{HashRuleRef: AnonymizedValueRegex}},
		Groups: []GroupSchema{{
			ID: "actions",
			Rules: &SchemeRules{
				EventID:   []string{EnumExpr("action.invoked")},
				EventData: map[string][]string{"session_id": {"{regexp#hash}"}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := newTestLogger(t, nil)
	logger.validator = validator
	logger.anonymizer = testAnonymizer([]string{"session_id"})

	logger.Track(Group("actions", 42), "action.invoked", map[string]any{"session_id": "abc"})

	events, err := logger.buf.ReadAndClear()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if got, want := events[0].Event.Data["session_id"], hash("abc"); got != want {
		t.Fatalf("session_id = %v, want %s", got, want)
	}
}

func TestLoggerAnonymizationPreservesNestedInput(t *testing.T) {
	logger := newTestLogger(t, nil)
	logger.anonymizer = testAnonymizer([]string{"session_id", "nested.session_id", "items.session_id", "ids"})
	makeData := func() map[string]any {
		return map[string]any{
			"session_id": "abc",
			"nested":     map[string]any{"session_id": "def"},
			"items":      []any{map[string]any{"session_id": "ghi"}, nil},
			"ids":        []any{"jkl", 123, nil},
		}
	}
	data := makeData()
	logger.Track(Group("actions", 42), "action.invoked", data)
	logger.Track(Group("actions", 42), "action.invoked", data)
	if !reflect.DeepEqual(data, makeData()) {
		t.Fatalf("Track changed its input: %#v", data)
	}
	events, err := logger.buf.ReadAndClear()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !reflect.DeepEqual(events[0].Event.Data, events[1].Event.Data) {
		t.Fatal("reusing the input changed the recorded hashes")
	}
	if events[0].Event.Data["session_id"] != hash("abc") {
		t.Fatal("session ID was not hashed exactly once")
	}
}
