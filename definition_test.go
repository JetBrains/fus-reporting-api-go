package fus

import (
	"encoding/json"
	"reflect"
	"testing"
)

func authDefinition() *Definition {
	return &Definition{
		Version: "1",
		Rules:   &SchemeRules{Enums: map[string][]string{"boolean": {"true", "false"}}},
		Groups: []GroupDefinition{{
			ID: "cli.auth", Version: 2, Description: "Authentication",
			Events: []EventDefinition{
				{ID: "login.completed", Description: "Login result", Fields: []FieldDefinition{
					{Path: "method", Rules: []string{EnumExpr("token", "guest")}},
					{Path: "success", Rules: []string{EnumRefExpr("boolean")}},
					{Path: "session_id", Rules: []string{RegexpExpr("[a-z]+")}, Anonymized: true},
				}},
				{ID: "token.loaded", Description: "Token source", Fields: []FieldDefinition{
					{Path: "source", Rules: []string{EnumExpr("env", "keyring")}},
					{Path: "method", Rules: []string{EnumExpr("cached")}},
					{Path: "session_id", Rules: []string{EnumExpr("none")}},
				}},
				{ID: "cleared", Description: "Credentials cleared"},
			},
		}},
	}
}

func TestDefinitionPreservesEventFields(t *testing.T) {
	d := authDefinition()
	es, err := d.BuildEventsScheme(RecorderConfig{RecorderID: "TCX", RecorderVersion: 1}, "1.0")
	if err != nil {
		t.Fatal(err)
	}
	g := es.Scheme[0]
	if g.Version != 2 || g.Type != GroupTypeCounter || g.Description != "Authentication" {
		t.Fatalf("group: %+v", g)
	}
	for i, want := range [][]string{{"method", "success", "session_id"}, {"source", "method", "session_id"}, {}} {
		got := []string{}
		for _, f := range g.Schema[i].Fields {
			got = append(got, f.Path)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: fields %v, want %v", g.Schema[i].Event, got, want)
		}
	}
	if g.Schema[2].Fields == nil {
		t.Fatal("fieldless event should serialize as [], not null")
	}
	if got := g.Schema[0].Fields[1].Value; !reflect.DeepEqual(got, []string{"{enum:true|false}"}) {
		t.Fatalf("local enum not resolved: %v", got)
	}
	if got := g.Schema[0].Fields[2]; !got.ShouldBeAnonymized || !reflect.DeepEqual(got.Value, []string{"{regexp#hash}"}) {
		t.Fatalf("hashed field: %+v", got)
	}
	if g.Schema[1].Fields[2].ShouldBeAnonymized {
		t.Fatal("anonymization leaked to another event")
	}
	a, _ := json.Marshal(es)
	var decoded EventsScheme
	if err := json.Unmarshal(a, &decoded); err != nil || !reflect.DeepEqual(es, &decoded) {
		t.Fatalf("registration JSON round trip: %v", err)
	}
	es2, _ := d.BuildEventsScheme(RecorderConfig{RecorderID: "TCX", RecorderVersion: 1}, "1.0")
	b, _ := json.Marshal(es2)
	if string(a) != string(b) {
		t.Fatal("unstable generation")
	}
}

func TestDefinitionFallback(t *testing.T) {
	d := authDefinition()
	s, err := d.BuildValidationScheme()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Scheme
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	s = &decoded
	v, err := NewValidator(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"token", "guest", "cached"} {
		e, dropped := v.Validate(LogEvent{Group: Group("cli.auth", 2), Event: EventAction{ID: "login.completed", Data: map[string]any{"method": method}}})
		if dropped || e.Event.Data["method"] != method {
			t.Fatalf("union rule rejected %s: %+v", method, e)
		}
	}
	for _, version := range []int{1, 3} {
		_, dropped := v.Validate(LogEvent{Group: Group("cli.auth", version)})
		if !dropped {
			t.Fatalf("fallback accepted undeclared version %d", version)
		}
	}
	l := newTestLogger(t, nil)
	l.validator = v
	l.anonymizer = NewAnonymizer(s, []byte(testFUSConfig.Salt))
	l.Track(Group("cli.auth", 2), "login.completed", map[string]any{"method": "guest", "session_id": "abc"})
	l.Track(Group("cli.auth", 2), "token.loaded", map[string]any{"session_id": "none"})
	events, err := l.buf.ReadAndClear()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("buffered %d events", len(events))
	}
	if len(events[0].Event.Data) != 2 || events[0].Group.Version != 2 {
		t.Fatalf("payload changed: %+v", events[0])
	}
	if events[0].Event.Data["session_id"] != Anonymize([]byte(testFUSConfig.Salt), "abc") {
		t.Fatal("declared field not hashed")
	}
	if events[1].Event.Data["session_id"] != "none" {
		t.Fatal("other event unexpectedly hashed")
	}
	s.Rules.Enums["boolean"][0] = "changed"
	if d.Rules.Enums["boolean"][0] != "true" {
		t.Fatal("fallback aliases declaration")
	}
}

func TestDefinitionSharedRegistrationRules(t *testing.T) {
	d := authDefinition()
	d.Rules.Regexps = map[string]string{"local": "[a-z]+"}
	d.Rules.Ranges = map[string]IntRange{"local": {From: 1, To: 5}}
	d.Groups[0].Events[0].Fields = []FieldDefinition{
		{Path: "name", Rules: []string{RegexpRefExpr("local")}},
		{Path: "count", Rules: []string{RangeRefExpr("local")}},
		{Path: "external", Rules: []string{RegexpRefExpr("external")}},
	}
	es, err := d.BuildEventsScheme(RecorderConfig{}, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"{regexp:[a-z]+}", "{range:1..5}", "{regexp#external}"} {
		if got := es.Scheme[0].Schema[0].Fields[i].Value; !reflect.DeepEqual(got, []string{want}) {
			t.Fatalf("rule %d = %v, want %s", i, got, want)
		}
	}
}

func TestDefinitionRejectsAmbiguousDeclarations(t *testing.T) {
	cases := map[string]func(*Definition){
		"duplicate group":     func(d *Definition) { d.Groups = append(d.Groups, d.Groups[0]) },
		"duplicate event":     func(d *Definition) { d.Groups[0].Events = append(d.Groups[0].Events, d.Groups[0].Events[0]) },
		"duplicate field":     func(d *Definition) { e := &d.Groups[0].Events[0]; e.Fields = append(e.Fields, e.Fields[0]) },
		"missing version":     func(d *Definition) { d.Groups[0].Version = 0 },
		"missing description": func(d *Definition) { d.Groups[0].Events[0].Description = "" },
		"overlapping path": func(d *Definition) {
			e := &d.Groups[0].Events[0]
			e.Fields = append(e.Fields, FieldDefinition{Path: "method.name", Rules: []string{EnumExpr("x")}})
		},
		"invalid type": func(d *Definition) { d.Groups[0].Events[0].Fields[0].DataType = "OBJECT" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := authDefinition()
			mutate(d)
			if _, err := d.BuildEventsScheme(RecorderConfig{}, ""); err == nil {
				t.Fatal("registration accepted invalid declaration")
			}
			if _, err := d.BuildValidationScheme(); err == nil {
				t.Fatal("fallback accepted invalid declaration")
			}
		})
	}
}

func TestDefinitionComplexFieldsAreRegistrationOnly(t *testing.T) {
	g := GroupDefinition{ID: "state", Version: 1, Type: GroupTypeState, Description: "State", Events: []EventDefinition{{ID: "snapshot", Description: "Snapshot", Fields: []FieldDefinition{
		{Path: "items.name", Rules: []string{EnumExpr("x")}},
		{Path: "tags", Rules: []string{EnumExpr("a")}, DataType: "ARRAY"},
	}}}}
	d := &Definition{Groups: []GroupDefinition{g}}
	es, err := d.BuildEventsScheme(RecorderConfig{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if es.Scheme[0].Type != GroupTypeState || es.Scheme[0].Schema[0].Fields[1].DataType != "ARRAY" {
		t.Fatalf("registration lost types: %+v", es)
	}
	if _, err := d.BuildValidationScheme(); err == nil {
		t.Fatal("silently built an unsupported runtime schema")
	}
}
