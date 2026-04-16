package fus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tcxScheme is a minimal scheme modelled on the TCX spec: global regexp/enum
// refs plus a handful of groups with inline and referenced rules.
func tcxScheme() *Scheme {
	return &Scheme{
		Version: "1",
		Rules: &SchemeRules{
			Enums: map[string][]string{
				"boolean": {"true", "false"},
			},
			Regexps: map[string]string{
				"integer": `-?(0|[1-9][0-9]*)`,
				"version": `\d+\.\d+\.\d+`,
			},
			Ranges: map[string]IntRange{
				"uint8": {From: 0, To: 255},
			},
		},
		Groups: []GroupSchema{
			{
				ID:     "teamcity.cli.session",
				Builds: []SchemeRange{{From: "0.1.0"}},
				Rules: &SchemeRules{
					EventID: []string{"enum:started"},
					EventData: map[string][]string{
						"os":          {"enum:darwin|linux|windows|other"},
						"cli_version": {"regexp#version"},
						"has_linked":  {"enum#boolean"},
					},
				},
			},
			{
				ID: "teamcity.cli.command",
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
}

func newTCXValidator(t *testing.T) *Validator {
	t.Helper()
	v, err := NewValidator(tcxScheme())
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

func TestValidatorAcceptsClean(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{
			ID: "started",
			Data: map[string]any{
				"os":          "darwin",
				"cli_version": "1.2.3",
				"has_linked":  true,
			},
		},
	}
	got, drop := v.Validate(ev)
	if drop {
		t.Fatal("clean event should not be dropped")
	}
	if got.Event.ID != "started" {
		t.Errorf("event.id = %q, want started", got.Event.ID)
	}
	if got.Event.Data["os"] != "darwin" {
		t.Errorf("os = %v, want darwin", got.Event.Data["os"])
	}
	if got.Event.Data["has_linked"] != true {
		t.Errorf("has_linked = %v, want true", got.Event.Data["has_linked"])
	}
	if got.Event.Data["cli_version"] != "1.2.3" {
		t.Errorf("cli_version = %v, want 1.2.3", got.Event.Data["cli_version"])
	}
}

func TestValidatorRejectsUnknownEnum(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "started", Data: map[string]any{"os": "plan9"}},
	}
	got, _ := v.Validate(ev)
	if got.Event.Data["os"] != "validation.unmatched_rule" {
		t.Errorf("unknown enum value should sentinel-replace, got %v", got.Event.Data["os"])
	}
}

func TestValidatorRejectsBadRegexp(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "started", Data: map[string]any{"cli_version": "not-semver"}},
	}
	got, _ := v.Validate(ev)
	if got.Event.Data["cli_version"] != "validation.unmatched_rule" {
		t.Errorf("bad regexp should sentinel-replace, got %v", got.Event.Data["cli_version"])
	}
}

func TestValidatorUnknownFieldKey(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "started", Data: map[string]any{"snooping_field": "secret"}},
	}
	got, _ := v.Validate(ev)
	if _, ok := got.Event.Data["snooping_field"]; ok {
		t.Errorf("unknown key should not reach the wire, got %v", got.Event.Data)
	}
	if got.Event.Data["validation.undefined_rule"] != "validation.undefined_rule" {
		t.Errorf("unknown key should map to sentinel entry, got %v", got.Event.Data)
	}
}

func TestValidatorUnknownGroup(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.unregistered", Version: 1},
		Event: EventAction{ID: "something", Data: map[string]any{"x": 1}},
	}
	got, drop := v.Validate(ev)
	if drop {
		t.Fatal("unknown group should not drop; should sentinel-replace")
	}
	if got.Event.ID != "validation.undefined_rule" {
		t.Errorf("event.id for unknown group = %q, want validation.undefined_rule", got.Event.ID)
	}
	if len(got.Event.Data) != 1 || got.Event.Data["validation.undefined_rule"] != "validation.undefined_rule" {
		t.Errorf("data for unknown group should be the undefined_rule marker, got %v", got.Event.Data)
	}
}

func TestValidatorDropsOutOfBuildRange(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.0.5", // below From=0.1.0
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "started"},
	}
	_, drop := v.Validate(ev)
	if !drop {
		t.Error("build below From should drop the event")
	}
}

func TestValidatorUnknownEventID(t *testing.T) {
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "unregistered_event"},
	}
	got, _ := v.Validate(ev)
	if got.Event.ID != "validation.unmatched_rule" {
		t.Errorf("unknown event ID = %q, want validation.unmatched_rule", got.Event.ID)
	}
}

func TestValidatorAllowsSystemEvents(t *testing.T) {
	v := newTCXValidator(t)
	for _, id := range []string{"registered", "invoked", "invocation.failed"} {
		ev := LogEvent{
			Build: "0.2.0",
			Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
			Event: EventAction{ID: id},
		}
		got, drop := v.Validate(ev)
		if drop {
			t.Errorf("system event %q should not drop", id)
		}
		if got.Event.ID != id {
			t.Errorf("system event %q rewritten to %q", id, got.Event.ID)
		}
	}
}

func TestValidatorRangeRule(t *testing.T) {
	v := newTCXValidator(t)
	good := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.command", Version: 1},
		Event: EventAction{ID: "executed", Data: map[string]any{"exit_code": 1, "bucket": 200}},
	}
	got, _ := v.Validate(good)
	if got.Event.Data["exit_code"] != 1 {
		t.Errorf("exit_code in range should pass, got %v", got.Event.Data["exit_code"])
	}
	if got.Event.Data["bucket"] != 200 {
		t.Errorf("bucket in uint8 should pass, got %v", got.Event.Data["bucket"])
	}

	bad := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.command", Version: 1},
		Event: EventAction{ID: "executed", Data: map[string]any{"exit_code": 99, "bucket": 300}},
	}
	got, _ = v.Validate(bad)
	if got.Event.Data["exit_code"] != "validation.unmatched_rule" {
		t.Errorf("exit_code out of range should sentinel, got %v", got.Event.Data["exit_code"])
	}
	if got.Event.Data["bucket"] != "validation.unmatched_rule" {
		t.Errorf("bucket out of uint8 should sentinel, got %v", got.Event.Data["bucket"])
	}
}

func TestValidatorSentinelPassthrough(t *testing.T) {
	// A value already containing a validator sentinel must be passed through
	// unchanged — matches JVM behavior and keeps round-trips stable.
	v := newTCXValidator(t)
	ev := LogEvent{
		Build: "0.2.0",
		Group: EventGroup{ID: "teamcity.cli.session", Version: 1},
		Event: EventAction{ID: "started", Data: map[string]any{"os": "validation.unmatched_rule"}},
	}
	got, _ := v.Validate(ev)
	if got.Event.Data["os"] != "validation.unmatched_rule" {
		t.Errorf("sentinel value should pass through, got %v", got.Event.Data["os"])
	}
}

func TestValidatorGroupLocalEnums(t *testing.T) {
	// CDN metadata uses group-local enums for event IDs (the __event_id pattern).
	// The validator must resolve enum#ref against group-local rules, not just globals.
	s := &Scheme{
		Version: "1",
		Rules: &SchemeRules{
			Enums: map[string][]string{"boolean": {"true", "false"}},
		},
		Groups: []GroupSchema{{
			ID: "my.group",
			Rules: &SchemeRules{
				EventID:   []string{"{enum#__event_id}"},
				EventData: map[string][]string{"enabled": {"{enum#boolean}"}},
				Enums: map[string][]string{
					"__event_id": {"click", "press"},
				},
			},
		}},
	}
	v, err := NewValidator(s)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	ev := LogEvent{
		Group: EventGroup{ID: "my.group", Version: 1},
		Event: EventAction{ID: "click", Data: map[string]any{"enabled": "true"}, Count: 1},
	}
	got, drop := v.Validate(ev)
	if drop {
		t.Fatal("event dropped")
	}
	if got.Event.ID != "click" {
		t.Errorf("event ID = %q, want click (group-local enum should resolve)", got.Event.ID)
	}
	if got.Event.Data["enabled"] != "true" {
		t.Errorf("enabled = %v, want true (global enum should still resolve)", got.Event.Data["enabled"])
	}

	// Unknown event ID should be rejected.
	ev.Event.ID = "swipe"
	got, _ = v.Validate(ev)
	if got.Event.ID != "validation.unmatched_rule" {
		t.Errorf("unknown event ID = %q, want sentinel", got.Event.ID)
	}
}

func TestRuleExprHelpers(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{EnumExpr("a", "b", "c"), "enum:a|b|c"},
		{EnumExpr("only"), "enum:only"},
		{EnumRefExpr("boolean"), "enum#boolean"},
		{RegexpExpr(`\d+`), `regexp:\d+`},
		{RegexpRefExpr("version"), "regexp#version"},
		{RangeExpr(0, 255), "range:0..255"},
		{RangeExpr(-5, 5), "range:-5..5"},
		{RangeRefExpr("uint8"), "range#uint8"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}

	// Round-trip: helpers produce expressions that parseRuleExpr accepts.
	g := &globalRules{}
	for _, expr := range []string{
		EnumExpr("a", "b"),
		RegexpExpr(`\d+`),
		RangeExpr(0, 10),
	} {
		r := parseRuleExpr(expr, g)
		if _, ok := r.(incorrectRuleStub); ok {
			t.Errorf("parseRuleExpr(%q) returned incorrectRuleStub", expr)
		}
	}
}

func TestWriteSchemeJSON(t *testing.T) {
	s := &Scheme{
		Version: "1",
		Rules: &SchemeRules{
			Enums: map[string][]string{"boolean": {"true", "false"}},
		},
		Groups: []GroupSchema{
			{
				ID: "cli.command",
				Rules: &SchemeRules{
					EventID: []string{EnumExpr("executed")},
					EventData: map[string][]string{
						"exit_code": {RangeExpr(0, 2)},
					},
				},
			},
		},
	}
	var buf strings.Builder
	if err := WriteSchemeJSON(s, &buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	// Indented, includes our expected pieces.
	if !strings.Contains(out, `"id": "cli.command"`) {
		t.Errorf("missing group id in output: %s", out)
	}
	if !strings.Contains(out, `"enum:executed"`) {
		t.Errorf("missing enum expression in output: %s", out)
	}
	if !strings.Contains(out, `"range:0..2"`) {
		t.Errorf("missing range expression in output: %s", out)
	}

	// Round-trip: written JSON re-parses into an equivalent *Scheme.
	var rt Scheme
	if err := json.Unmarshal([]byte(out), &rt); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !reflect.DeepEqual(rt.Groups[0].Rules.EventData, s.Groups[0].Rules.EventData) {
		t.Errorf("round-trip EventData mismatch\n got %v\nwant %v", rt.Groups[0].Rules.EventData, s.Groups[0].Rules.EventData)
	}
}

func TestPermissiveValidatorPassesEverything(t *testing.T) {
	v := newPermissiveValidator()
	ev := LogEvent{
		Build: "0.0.1",
		Group: EventGroup{ID: "anything", Version: 1},
		Event: EventAction{ID: "arbitrary", Data: map[string]any{"weird.key": "weird value"}},
	}
	got, drop := v.Validate(ev)
	if drop {
		t.Fatal("permissive validator should not drop events")
	}
	if got.Event.ID != "arbitrary" || got.Event.Data["weird.key"] != "weird value" {
		t.Errorf("permissive validator mutated event: %+v", got)
	}
}

func TestLoadSchemeFromFile(t *testing.T) {
	s := tcxScheme()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSchemeFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got.Version, s.Version) {
		t.Errorf("version mismatch: %v vs %v", got.Version, s.Version)
	}
	if len(got.Groups) != len(s.Groups) {
		t.Errorf("groups len mismatch: %d vs %d", len(got.Groups), len(s.Groups))
	}
}

func TestParseIntRangeExpr(t *testing.T) {
	tests := []struct {
		in   string
		want IntRange
		ok   bool
	}{
		{"0..2", IntRange{0, 2}, true},
		{"10..100", IntRange{10, 100}, true},
		{"-5..5", IntRange{-5, 5}, true},
		{"bad", IntRange{}, false},
		{"1..a", IntRange{}, false},
		{"1", IntRange{}, false},
	}
	for _, tt := range tests {
		got, ok := parseIntRangeExpr(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseIntRangeExpr(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCompareBuilds(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0", "1.0.0", 0},
		{"1.0.1", "1.0", 1},
	}
	for _, c := range cases {
		got := compareBuilds(c.a, c.b)
		if (c.want < 0 && got >= 0) || (c.want > 0 && got <= 0) || (c.want == 0 && got != 0) {
			t.Errorf("compareBuilds(%q,%q) sign = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestValueToString(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{int64(-7), "-7"},
		{float64(12), "12"},
		{float64(1.5), "1.5"},
	}
	for _, tt := range tests {
		if got := valueToString(tt.in); got != tt.want {
			t.Errorf("valueToString(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- integration with Logger ---

func TestLoggerRunsValidator(t *testing.T) {
	validator, err := NewValidator(tcxScheme())
	if err != nil {
		t.Fatal(err)
	}

	logger, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TCX",
			RecorderVersion: 1,
			ProductCode:     "TCX",
			BuildVersion:    "0.2.0",
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(&FUSConfig{SendEndpoint: "http://localhost", Salt: "test-salt"}),
		WithValidator(validator),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Event with one unknown key and one bad-value key.
	logger.Track(
		EventGroup{ID: "teamcity.cli.session", Version: 1},
		"started",
		map[string]any{
			"os":          "darwin", // valid
			"cli_version": "oops",   // unmatched regexp#version
			"secret":      "leak",   // unknown key
		},
	)

	events, err := logger.buf.ReadAndClear()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	d := events[0].Event.Data
	if d["os"] != "darwin" {
		t.Errorf("os = %v, want darwin", d["os"])
	}
	if d["cli_version"] != "validation.unmatched_rule" {
		t.Errorf("cli_version = %v, want unmatched sentinel", d["cli_version"])
	}
	if _, ok := d["secret"]; ok {
		t.Error("unknown key 'secret' leaked to wire")
	}
	if d["validation.undefined_rule"] != "validation.undefined_rule" {
		t.Errorf("unknown key should collapse to undefined sentinel, got %v", d)
	}
}

func TestLoggerValidatorDropsOutOfRangeBuild(t *testing.T) {
	validator, _ := NewValidator(tcxScheme())
	logger, err := NewLogger(
		t.Context(),
		RecorderConfig{
			RecorderID:      "TCX",
			RecorderVersion: 1,
			ProductCode:     "TCX",
			BuildVersion:    "0.0.1", // below 0.1.0
			DataDir:         t.TempDir(),
		},
		WithFUSConfig(&FUSConfig{SendEndpoint: "http://localhost", Salt: "test-salt"}),
		WithValidator(validator),
	)
	if err != nil {
		t.Fatal(err)
	}

	logger.Track(EventGroup{ID: "teamcity.cli.session", Version: 1}, "started", map[string]any{"os": "darwin"})

	events, _ := logger.buf.ReadAndClear()
	if len(events) != 0 {
		t.Errorf("expected event to be dropped (build out of range), got %d buffered", len(events))
	}
}
