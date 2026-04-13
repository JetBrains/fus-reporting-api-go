package fus

import (
	"reflect"
	"testing"
)

func TestEscape(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"hello world", "hello_world"},        // space -> _
		{"a:b;c,d", "a_b_c_d"},                // separators -> _
		{"foo.bar", "foo.bar"},                // . is allowed in identifier scope
		{"tab\there", "tab_here"},             // \t -> _
		{"line\nfeed", "line_feed"},           // \n -> _
		{"with\"quote", "withquote"},          // " dropped
		{"with'apos", "withapos"},             // ' dropped
		{"café", "caf?"},                      // non-ASCII -> ?
		{"null\x00byte", "null_byte"},         // control -> _
		{"del\x7fchar", "del_char"},           // DEL -> _
		{"", ""},
	}
	for _, tt := range tests {
		if got := Escape(tt.in); got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapeEventIDOrFieldValue(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"hello world", "hello world"}, // space preserved
		{"tab\there", "tab here"},      // \t -> space
		{"line\nfeed", "line feed"},    // \n -> space
		{"cr\rhere", "cr here"},
		{"a:b;c,d", "a:b;c,d"},      // separators preserved in values
		{"foo.bar", "foo.bar"},      // . preserved
		{"with\"quote", "withquote"}, // " dropped
		{"null\x00byte", "null_byte"},
		{"café", "caf?"},
	}
	for _, tt := range tests {
		if got := EscapeEventIDOrFieldValue(tt.in); got != tt.want {
			t.Errorf("EscapeEventIDOrFieldValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapeFieldName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"plain_field", "plain_field"},
		{"user.email", "user_email"},    // . -> _
		{"a b", "a_b"},                  // space -> _
		{"foo:bar", "foo_bar"},
		{"third.party", "third.party"}, // sentinel preserved
		{"validation.unmatched_rule", "validation.unmatched_rule"},
	}
	for _, tt := range tests {
		if got := EscapeFieldName(tt.in); got != tt.want {
			t.Errorf("EscapeFieldName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapeEventData(t *testing.T) {
	in := map[string]any{
		"normal":      "value",
		"user.email":  "foo@bar",
		"with spaces": "has\ttab",
		"count":       42,
		"flag":        true,
		"nested": map[string]any{
			"a.b": "c d",
		},
		"list": []any{"one two", 3, nil, "four\nfive"},
	}
	got := EscapeEventData(in)
	want := map[string]any{
		"normal":      "value",
		"user_email":  "foo@bar",
		"with_spaces": "has tab",
		"count":       42,
		"flag":        true,
		"nested": map[string]any{
			"a_b": "c d",
		},
		"list": []any{"one two", 3, nil, "four five"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EscapeEventData mismatch\n got: %#v\nwant: %#v", got, want)
	}

	// Original map is untouched.
	if _, ok := in["user_email"]; ok {
		t.Error("EscapeEventData mutated the input map")
	}
}

func TestEscapeIDs(t *testing.T) {
	in := map[string]string{
		"device":       "abc",
		"server.id":    "tc prod",
		"with\tspace":  "v\nl",
	}
	got := EscapeIDs(in)
	want := map[string]string{
		"device":      "abc",
		"server_id":   "tc prod",
		"with_space":  "v l",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EscapeIDs mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestEscapeNilMaps(t *testing.T) {
	if got := EscapeEventData(nil); got != nil {
		t.Errorf("EscapeEventData(nil) = %v, want nil", got)
	}
	if got := EscapeIDs(nil); got != nil {
		t.Errorf("EscapeIDs(nil) = %v, want nil", got)
	}
}

func TestEscapeIsPureCopy(t *testing.T) {
	// If nothing needs escaping, escapeInternal returns the input unchanged.
	s := "clean_field"
	if got := Escape(s); got != s {
		t.Errorf("Escape(%q) = %q, want %q", s, got, s)
	}
}
