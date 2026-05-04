package fus

import (
	"testing"
)

func TestBuildEventsScheme(t *testing.T) {
	s := &Scheme{
		Version: "1",
		Rules: &SchemeRules{
			Enums:   map[string][]string{"boolean": {"true", "false"}},
			Regexps: map[string]string{"uuid": `[0-9a-f-]+`},
		},
		Groups: []GroupSchema{
			{
				ID:   "cli.session",
				Type: GroupTypeState,
				Rules: &SchemeRules{
					EventID: []string{EnumExpr("invoked")},
					EventData: map[string][]string{
						"session_id": {RegexpRefExpr("uuid")},
						"os":         {EnumExpr("darwin", "linux")},
					},
				},
				AnonymizedFields: []AnonymizedField{
					{Event: "invoked", Fields: []string{"session_id"}},
				},
			},
			{
				ID:   "cli.command",
				Type: GroupTypeCounter,
				Rules: &SchemeRules{
					EventID: []string{EnumExpr("executed")},
					EventData: map[string][]string{
						"command": {EnumExpr("run", "build")},
					},
				},
			},
		},
	}

	cfg := RecorderConfig{RecorderID: "TCX", RecorderVersion: 1}
	es, err := BuildEventsScheme(s, cfg, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	if es.BuildNumber != "0.1.0" {
		t.Errorf("buildNumber = %q", es.BuildNumber)
	}
	if len(es.Scheme) != 2 {
		t.Fatalf("groups = %d, want 2", len(es.Scheme))
	}

	session := es.Scheme[0]
	if session.Type != GroupTypeState {
		t.Errorf("session type = %q, want state", session.Type)
	}
	if session.Recorder != "TCX" {
		t.Errorf("recorder = %q", session.Recorder)
	}
	if len(session.Schema) != 1 || session.Schema[0].Event != "invoked" {
		t.Fatalf("session events = %v", session.Schema)
	}

	// session_id should be anonymized
	var sidField *ESFieldDescriptor
	for i := range session.Schema[0].Fields {
		if session.Schema[0].Fields[i].Path == "session_id" {
			sidField = &session.Schema[0].Fields[i]
			break
		}
	}
	if sidField == nil {
		t.Fatal("session_id field not found")
	}
	if !sidField.ShouldBeAnonymized {
		t.Error("session_id.shouldBeAnonymized = false, want true")
	}
	if len(sidField.Value) != 2 || sidField.Value[0] != "{regexp#uuid}" {
		t.Errorf("session_id.value = %v, want [{regexp#uuid}, {regexp:%s}]", sidField.Value, AnonymizedValueRegex)
	}
	if sidField.Value[1] != "{regexp:"+AnonymizedValueRegex+"}" {
		t.Errorf("anonymized field missing auto-injected hash rule; got %v", sidField.Value)
	}

	// os should not be anonymized
	var osField *ESFieldDescriptor
	for i := range session.Schema[0].Fields {
		if session.Schema[0].Fields[i].Path == "os" {
			osField = &session.Schema[0].Fields[i]
			break
		}
	}
	if osField == nil {
		t.Fatal("os field not found")
	}
	if osField.ShouldBeAnonymized {
		t.Error("os.shouldBeAnonymized = true, want false")
	}

	// command group should be counter
	cmd := es.Scheme[1]
	if cmd.Type != GroupTypeCounter {
		t.Errorf("command type = %q, want counter", cmd.Type)
	}

	// recorder scheme
	if len(es.RecorderScheme) != 1 {
		t.Fatalf("recorderScheme = %d, want 1", len(es.RecorderScheme))
	}
	rec := es.RecorderScheme[0]
	if rec.RecorderID != "TCX" || rec.RecorderVersion != 1 {
		t.Errorf("recorder = %s/%d", rec.RecorderID, rec.RecorderVersion)
	}
	if len(rec.IDs) != 1 || rec.IDs[0].Path != "device" || !rec.IDs[0].Required {
		t.Errorf("ids = %v", rec.IDs)
	}
}
