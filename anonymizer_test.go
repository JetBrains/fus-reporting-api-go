package fus

import (
	"testing"
)

var testSalt = []byte("testSalt")

func testAnonymizer(fields []string) *Anonymizer {
	return NewAnonymizer(&Scheme{
		Groups: []GroupSchema{{
			ID:               "actions",
			AnonymizedFields: []AnonymizedField{{Event: "action.invoked", Fields: fields}},
		}},
	}, testSalt)
}

func anonTestEvent(data map[string]any) LogEvent {
	return LogEvent{
		Group: EventGroup{ID: "actions", Version: 42},
		Event: EventAction{ID: "action.invoked", Data: data, Count: 5},
	}
}

func hash(s string) string { return Anonymize(testSalt, s) }

func TestAnonymizer_ClientAnonymization(t *testing.T) {
	a := testAnonymizer([]string{"project_id", "repository_id"})
	ev := anonTestEvent(map[string]any{"project_id": "/username/123", "repository_id": 456})
	a.AnonymizeEvent(&ev)

	if ev.Event.Data["project_id"] != hash("/username/123") {
		t.Errorf("project_id = %v, want %s", ev.Event.Data["project_id"], hash("/username/123"))
	}
	if ev.Event.Data["repository_id"] != hash("456") {
		t.Errorf("repository_id = %v, want %s", ev.Event.Data["repository_id"], hash("456"))
	}
}

func TestAnonymizer_ListAnonymization(t *testing.T) {
	a := testAnonymizer([]string{"project_id"})
	ev := anonTestEvent(map[string]any{"project_id": []any{"/username/123", 456}})
	a.AnonymizeEvent(&ev)

	list := ev.Event.Data["project_id"].([]any)
	if list[0] != hash("/username/123") {
		t.Errorf("list[0] = %v, want %s", list[0], hash("/username/123"))
	}
	if list[1] != hash("456") {
		t.Errorf("list[1] = %v, want %s", list[1], hash("456"))
	}
}

func TestAnonymizer_ObjectAnonymization(t *testing.T) {
	a := testAnonymizer([]string{"obj.foo", "obj.bar"})
	ev := anonTestEvent(map[string]any{"obj": map[string]any{"foo": "foo", "bar": 123}})
	a.AnonymizeEvent(&ev)

	obj := ev.Event.Data["obj"].(map[string]any)
	if obj["foo"] != hash("foo") {
		t.Errorf("obj.foo = %v, want %s", obj["foo"], hash("foo"))
	}
	if obj["bar"] != hash("123") {
		t.Errorf("obj.bar = %v, want %s", obj["bar"], hash("123"))
	}
}

func TestAnonymizer_ObjectsListAnonymization(t *testing.T) {
	a := testAnonymizer([]string{"obj.foo"})
	ev := anonTestEvent(map[string]any{
		"obj": []any{
			map[string]any{"foo": "foo1"},
			map[string]any{"foo": 123},
		},
	})
	a.AnonymizeEvent(&ev)

	list := ev.Event.Data["obj"].([]any)
	obj0 := list[0].(map[string]any)
	obj1 := list[1].(map[string]any)
	if obj0["foo"] != hash("foo1") {
		t.Errorf("list[0].foo = %v, want %s", obj0["foo"], hash("foo1"))
	}
	if obj1["foo"] != hash("123") {
		t.Errorf("list[1].foo = %v, want %s", obj1["foo"], hash("123"))
	}
}

func TestAnonymizer_NotHashedFields(t *testing.T) {
	a := testAnonymizer(nil)
	data := map[string]any{"project_id": "/username/123", "foo": []any{"foo", "bar"}}
	ev := anonTestEvent(data)
	a.AnonymizeEvent(&ev)

	if ev.Event.Data["project_id"] != "/username/123" {
		t.Error("non-declared field should not be anonymized")
	}
}

func TestAnonymizer_BlankStringHashing(t *testing.T) {
	a := testAnonymizer([]string{"foo"})
	ev := anonTestEvent(map[string]any{"foo": ""})
	a.AnonymizeEvent(&ev)

	if ev.Event.Data["foo"] != "" {
		t.Errorf("blank string should pass through, got %v", ev.Event.Data["foo"])
	}
}
