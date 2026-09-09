package fus

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Definition declares groups, events and their fields, like Kotlin EventLogGroup.
type Definition struct {
	Version string
	Rules   *SchemeRules // shared enum, regexp and range definitions
	Groups  []GroupDefinition
}

type GroupDefinition struct {
	ID          string
	Version     int
	Type        GroupType
	Description string
	Events      []EventDefinition
}

type EventDefinition struct {
	ID          string
	Description string
	Fields      []FieldDefinition
}

// FieldDefinition describes a leaf; dotted paths represent nested objects.
type FieldDefinition struct {
	Path        string
	Rules       []string
	DataType    string // PRIMITIVE (default) or ARRAY
	Anonymized  bool
	Description string
}

// Validate checks declaration structure before either output is generated.
func (d *Definition) Validate() error {
	if d == nil {
		return fmt.Errorf("fus: nil definition")
	}
	if d.Rules != nil && (len(d.Rules.EventID) != 0 || len(d.Rules.EventData) != 0) {
		return fmt.Errorf("fus: declare event IDs and fields in Groups, not Definition.Rules")
	}
	groups := map[string]bool{}
	for _, g := range d.Groups {
		if g.ID == "" || groups[g.ID] {
			return fmt.Errorf("fus: empty or duplicate group ID %q", g.ID)
		}
		groups[g.ID] = true
		if g.Version < 1 || g.Version == math.MaxInt || strings.TrimSpace(g.Description) == "" {
			return fmt.Errorf("fus: group %q requires a positive version and description", g.ID)
		}
		if g.Type != "" && g.Type != GroupTypeCounter && g.Type != GroupTypeState {
			return fmt.Errorf("fus: invalid group type %q", g.Type)
		}
		events := map[string]bool{}
		for _, e := range g.Events {
			if e.ID == "" || events[e.ID] || strings.Contains(e.ID, "|") || strings.TrimSpace(e.Description) == "" {
				return fmt.Errorf("fus: invalid or duplicate event %q in group %q (description required)", e.ID, g.ID)
			}
			events[e.ID] = true
			fields := map[string]bool{}
			for _, f := range e.Fields {
				if f.Path == "" || fields[f.Path] || len(f.Rules) == 0 {
					return fmt.Errorf("fus: invalid or duplicate field %q in %s/%s (rules required)", f.Path, g.ID, e.ID)
				}
				fields[f.Path] = true
				for _, part := range strings.Split(f.Path, ".") {
					if part == "" {
						return fmt.Errorf("fus: invalid field path %q", f.Path)
					}
				}
				if f.DataType != "" && f.DataType != "PRIMITIVE" && f.DataType != "ARRAY" {
					return fmt.Errorf("fus: invalid data type %q for %s/%s/%s", f.DataType, g.ID, e.ID, f.Path)
				}
			}
			for path := range fields {
				parts := strings.Split(path, ".")
				for i := 1; i < len(parts); i++ {
					if fields[strings.Join(parts[:i], ".")] {
						return fmt.Errorf("fus: overlapping field paths in %s/%s: %s", g.ID, e.ID, path)
					}
				}
			}
		}
	}
	return nil
}

// BuildEventsScheme generates registration metadata from each event's own fields.
func (d *Definition) BuildEventsScheme(cfg RecorderConfig, buildNumber string) (*EventsScheme, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	es := &EventsScheme{BuildNumber: buildNumber, Scheme: []ESGroupDescriptor{}, RecorderScheme: recorderScheme(cfg)}
	for _, g := range d.Groups {
		gd := ESGroupDescriptor{ID: g.ID, Type: g.Type, Version: g.Version, Description: g.Description, Recorder: cfg.RecorderID, Schema: []ESEventDescriptor{}}
		if gd.Type == "" {
			gd.Type = GroupTypeCounter
		}
		for _, e := range g.Events {
			ed := ESEventDescriptor{Event: e.ID, Description: e.Description, Fields: []ESFieldDescriptor{}}
			for _, f := range e.Fields {
				fd := ESFieldDescriptor{Path: f.Path, Value: d.registrationRules(f.Rules), DataType: f.DataType, ShouldBeAnonymized: f.Anonymized, Description: f.Description}
				if fd.DataType == "" {
					fd.DataType = "PRIMITIVE"
				}
				if f.Anonymized {
					fd.Value = []string{"{regexp#" + HashRuleRef + "}"}
				}
				ed.Fields = append(ed.Fields, fd)
			}
			gd.Schema = append(gd.Schema, ed)
		}
		es.Scheme = append(es.Scheme, gd)
	}
	return es, nil
}

// registrationRules inlines local references and preserves external AP references.
func (d *Definition) registrationRules(rules []string) []string {
	out := wrapBraces(rules)
	if d.Rules == nil {
		return out
	}
	for i, expr := range out {
		s := strings.TrimSuffix(strings.TrimPrefix(expr, "{"), "}")
		if ref, ok := strings.CutPrefix(s, "enum#"); ok {
			if values, found := d.Rules.Enums[ref]; found {
				out[i] = "{" + EnumExpr(values...) + "}"
			}
		}
		if ref, ok := strings.CutPrefix(s, "regexp#"); ok {
			if pattern, found := d.Rules.Regexps[ref]; found {
				out[i] = "{" + RegexpExpr(pattern) + "}"
			}
		}
		if ref, ok := strings.CutPrefix(s, "range#"); ok {
			if bounds, found := d.Rules.Ranges[ref]; found {
				out[i] = "{" + RangeExpr(bounds.From, bounds.To) + "}"
			}
		}
	}
	return out
}

// BuildValidationScheme merges event rules per group for an embedded fallback.
func (d *Definition) BuildValidationScheme() (*Scheme, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	s := &Scheme{Version: d.Version, Rules: cloneDefinitionRules(d.Rules), Groups: []GroupSchema{}}
	for _, g := range d.Groups {
		gs := GroupSchema{ID: g.ID, Type: g.Type, Versions: []SchemeRange{{From: strconv.Itoa(g.Version), To: strconv.Itoa(g.Version + 1)}}, Rules: &SchemeRules{EventData: map[string][]string{}}}
		if gs.Type == "" {
			gs.Type = GroupTypeCounter
		}
		for _, e := range g.Events {
			gs.Rules.EventID = append(gs.Rules.EventID, EnumExpr(e.ID))
			af := AnonymizedField{Event: e.ID}
			for _, f := range e.Fields {
				if f.DataType == "ARRAY" || strings.Contains(f.Path, ".") {
					return nil, fmt.Errorf("fus: runtime validation does not support array/nested field %s/%s/%s", g.ID, e.ID, f.Path)
				}
				for _, rule := range wrapBraces(f.Rules) {
					if !slices.Contains(gs.Rules.EventData[f.Path], rule) {
						gs.Rules.EventData[f.Path] = append(gs.Rules.EventData[f.Path], rule)
					}
				}
				if f.Anonymized {
					af.Fields = append(af.Fields, f.Path)
				}
			}
			if len(af.Fields) != 0 {
				gs.AnonymizedFields = append(gs.AnonymizedFields, af)
			}
		}
		s.Groups = append(s.Groups, gs)
	}
	return s, nil
}

func cloneDefinitionRules(r *SchemeRules) *SchemeRules {
	if r == nil {
		return nil
	}
	c := &SchemeRules{Enums: map[string][]string{}, Regexps: map[string]string{}, Ranges: map[string]IntRange{}}
	for k, v := range r.Enums {
		c.Enums[k] = slices.Clone(v)
	}
	for k, v := range r.Regexps {
		c.Regexps[k] = v
	}
	for k, v := range r.Ranges {
		c.Ranges[k] = v
	}
	return c
}
