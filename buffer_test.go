package fus

import (
	"sync"
	"testing"
)

func testEvent(id string) LogEvent {
	return LogEvent{
		Recorder: Recorder{ID: "TC", Version: 1},
		Product:  "TCC",
		IDs:      map[string]string{"device": "test#C"},
		Group:    EventGroup{ID: "test", Version: 1},
		Event:    EventAction{ID: id, Count: 1},
	}
}

func TestBufferAppendAndRead(t *testing.T) {
	buf := newBuffer(t.TempDir(), 100)

	if err := buf.Append(testEvent("e1")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := buf.Append(testEvent("e2")); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := buf.ReadAndClear()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Event.ID != "e1" {
		t.Errorf("events[0].event.id = %q, want e1", events[0].Event.ID)
	}
	if events[1].Event.ID != "e2" {
		t.Errorf("events[1].event.id = %q, want e2", events[1].Event.ID)
	}
}

func TestBufferReadAndClearTruncates(t *testing.T) {
	buf := newBuffer(t.TempDir(), 100)

	if err := buf.Append(testEvent("e1")); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := buf.ReadAndClear()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("first read events = %d, want 1", len(events))
	}

	events, err = buf.ReadAndClear()
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("second read events = %d, want 0", len(events))
	}
}

func TestBufferReadEmptyFile(t *testing.T) {
	buf := newBuffer(t.TempDir(), 100)

	events, err := buf.ReadAndClear()
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil for nonexistent file, got %v", events)
	}
}

func TestBufferTrim(t *testing.T) {
	buf := newBuffer(t.TempDir(), 3)

	for i := range 5 {
		if err := buf.Append(testEvent("e" + string(rune('0'+i)))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	if err := buf.Trim(); err != nil {
		t.Fatalf("trim: %v", err)
	}

	events, err := buf.ReadAndClear()
	if err != nil {
		t.Fatalf("read after trim: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events after trim = %d, want 3", len(events))
	}
}

func TestBufferConcurrentAppend(t *testing.T) {
	buf := newBuffer(t.TempDir(), 1000)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			for j := range 5 {
				_ = buf.Append(testEvent("e" + string(rune('A'+i)) + string(rune('0'+j))))
			}
		})
	}
	wg.Wait()

	events, err := buf.ReadAndClear()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 100 {
		t.Errorf("events = %d, want 100", len(events))
	}
}
