package fus

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Scheme mirrors the AP metadata JSON (EventGroupRemoteDescriptors on the JVM side).
// Each recorder publishes one of these in the Data Office metadata repo.
type Scheme struct {
	Version string        `json:"version,omitempty"`
	Rules   *SchemeRules  `json:"rules,omitempty"`
	Groups  []GroupSchema `json:"groups"`
}

// GroupSchema is one EventGroupRemoteDescriptor: the per-group rules plus the
// build/version ranges that gate whether an event for this group is accepted.
type GroupSchema struct {
	ID               string            `json:"id"`
	Builds           []SchemeRange     `json:"builds,omitempty"`
	Versions         []SchemeRange     `json:"versions,omitempty"`
	Rules            *SchemeRules      `json:"rules,omitempty"`
	AnonymizedFields []AnonymizedField `json:"anonymized_fields,omitempty"`
}

// AnonymizedField declares which event_data fields should be hashed for a
// specific event ID within its group.
type AnonymizedField struct {
	Event  string   `json:"event"`
	Fields []string `json:"fields"`
}

// SchemeRules carries either group-local or global (scheme-level) rule bundles.
//
// At group scope, EventID and EventData drive per-event validation.
// At scheme scope, Enums/Regexps/Ranges are global reference tables looked up
// by {enum#ref}/{regexp#ref}/{range#ref} expressions in group rules.
type SchemeRules struct {
	EventID   []string            `json:"event_id,omitempty"`
	EventData map[string][]string `json:"event_data,omitempty"`
	Enums     map[string][]string `json:"enums,omitempty"`
	Regexps   map[string]string   `json:"regexps,omitempty"`
	Ranges    map[string]IntRange `json:"ranges,omitempty"`
}

// IntRange is an inclusive [from, to] integer range used by range rules.
type IntRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// SchemeRange carries a build or version range used to gate a group.
// Empty From or To is treated as open-ended.
type SchemeRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// LoadSchemeFromFile parses a scheme JSON file from disk.
func LoadSchemeFromFile(path string) (*Scheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scheme: %w", err)
	}
	var s Scheme
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse scheme: %w", err)
	}
	return &s, nil
}

// WriteSchemeJSON serializes a scheme to w in the AP metadata repo format
// (indented, 2-space). A product typically wires this into a `go run`-based
// code generator whose output (schema.json) is what Data Office registers.
func WriteSchemeJSON(s *Scheme, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(s)
}

// Rule-expression helpers. Each returns the single string that belongs inside
// a SchemeRules.EventID or SchemeRules.EventData[key] slice. They mirror the
// JVM ValidationSimpleRuleFactory grammar — the same syntax the server-side
// validator expects in the metadata JSON.

// EnumExpr returns an inline enum rule: "enum:a|b|c".
func EnumExpr(values ...string) string {
	return "enum:" + strings.Join(values, "|")
}

// EnumRefExpr returns an enum reference to the scheme-global Rules.Enums[ref].
func EnumRefExpr(ref string) string {
	return "enum#" + ref
}

// RegexpExpr returns an inline regexp rule: "regexp:<pattern>".
// The pattern is anchored automatically by the validator.
func RegexpExpr(pattern string) string {
	return "regexp:" + pattern
}

// RegexpRefExpr returns a regexp reference to the scheme-global Rules.Regexps[ref].
func RegexpRefExpr(ref string) string {
	return "regexp#" + ref
}

// RangeExpr returns an inline integer range rule: "range:<from>..<to>".
func RangeExpr(from, to int) string {
	return "range:" + strconv.Itoa(from) + ".." + strconv.Itoa(to)
}

// RangeRefExpr returns a range reference to the scheme-global Rules.Ranges[ref].
func RangeRefExpr(ref string) string {
	return "range#" + ref
}
