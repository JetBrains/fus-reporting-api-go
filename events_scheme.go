package fus

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// EventsScheme is the product schema JSON that the FUS metadata team uses to
// generate metadata entries. Attach this to YT issues in the FUS project when
// requesting metadata changes.
type EventsScheme struct {
	BuildNumber    string              `json:"buildNumber,omitempty"`
	Scheme         []ESGroupDescriptor `json:"scheme"`
	CommitHash     string              `json:"commitHash,omitempty"`
	RecorderScheme []ESRecorderDesc    `json:"recorderScheme,omitempty"`
}

type ESGroupDescriptor struct {
	ID          string              `json:"id"`
	Type        GroupType           `json:"type"`
	Version     int                 `json:"version"`
	Schema      []ESEventDescriptor `json:"schema"`
	Description string              `json:"description,omitempty"`
	Recorder    string              `json:"recorder,omitempty"`
}

type ESEventDescriptor struct {
	Event       string              `json:"event"`
	Fields      []ESFieldDescriptor `json:"fields"`
	Description string              `json:"description,omitempty"`
}

type ESFieldDescriptor struct {
	Path               string   `json:"path"`
	Value              []string `json:"value"`
	DataType           string   `json:"dataType"`
	ShouldBeAnonymized bool     `json:"shouldBeAnonymized"`
	Description        string   `json:"description,omitempty"`
}

type ESRecorderDesc struct {
	RecorderID      string                `json:"recorderId"`
	RecorderVersion int                   `json:"recorderVersion"`
	IDs             []ESRecorderFieldDesc `json:"ids"`
	ClientData      []ESRecorderFieldDesc `json:"clientData"`
	SystemData      []ESRecorderFieldDesc `json:"systemData"`
}

type ESRecorderFieldDesc struct {
	Path     string   `json:"path"`
	Required bool     `json:"required"`
	Values   []string `json:"values,omitempty"`
}

// BuildEventsScheme converts a Scheme into the EventsScheme format used by the
// FUS metadata team. The recorder config supplies recorder-level fields (ids,
// client_data, system_data).
func BuildEventsScheme(s *Scheme, cfg RecorderConfig, buildNumber string) (*EventsScheme, error) {
	if s == nil {
		return nil, fmt.Errorf("fus: nil scheme")
	}

	es := &EventsScheme{
		BuildNumber: buildNumber,
	}

	for _, g := range s.Groups {
		anonByEvent := map[string]map[string]struct{}{}
		for _, af := range g.AnonymizedFields {
			m := make(map[string]struct{}, len(af.Fields))
			for _, f := range af.Fields {
				m[f] = struct{}{}
			}
			anonByEvent[af.Event] = m
		}

		gd := ESGroupDescriptor{
			ID:       g.ID,
			Type:     g.Type,
			Version:  extractGroupVersion(g),
			Recorder: cfg.RecorderID,
		}
		if gd.Type == "" {
			gd.Type = GroupTypeCounter
		}

		eventIDs := extractEventIDs(g.Rules)
		for _, eid := range eventIDs {
			ed := ESEventDescriptor{Event: eid}
			anonFields := anonByEvent[eid]

			if g.Rules != nil {
				for path, rules := range g.Rules.EventData {
					fd := ESFieldDescriptor{
						Path:               path,
						Value:              wrapBraces(rules),
						DataType:           "PRIMITIVE",
						ShouldBeAnonymized: isAnonymized(path, anonFields),
					}
					ed.Fields = append(ed.Fields, fd)
				}
			}
			gd.Schema = append(gd.Schema, ed)
		}
		es.Scheme = append(es.Scheme, gd)
	}

	es.RecorderScheme = []ESRecorderDesc{{
		RecorderID:      cfg.RecorderID,
		RecorderVersion: cfg.RecorderVersion,
		IDs:             []ESRecorderFieldDesc{{Path: "device", Required: true}},
		ClientData:      []ESRecorderFieldDesc{},
		SystemData:      []ESRecorderFieldDesc{},
	}}

	return es, nil
}

// WriteEventsSchemeJSON serializes an EventsScheme to w as indented JSON.
func WriteEventsSchemeJSON(es *EventsScheme, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(es)
}

func extractGroupVersion(g GroupSchema) int {
	if len(g.Versions) > 0 && g.Versions[0].From != "" {
		var v int
		fmt.Sscanf(g.Versions[0].From, "%d", &v)
		return v
	}
	return 1
}

func extractEventIDs(rules *SchemeRules) []string {
	if rules == nil || len(rules.EventID) == 0 {
		return nil
	}
	var ids []string
	for _, expr := range rules.EventID {
		s := strings.TrimSpace(expr)
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			s = s[1 : len(s)-1]
		}
		if after, ok := strings.CutPrefix(s, "enum:"); ok {
			ids = append(ids, strings.Split(after, "|")...)
		}
	}
	return ids
}

func wrapBraces(rules []string) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		if !strings.HasPrefix(r, "{") {
			out[i] = "{" + r + "}"
		} else {
			out[i] = r
		}
	}
	return out
}

func isAnonymized(path string, fields map[string]struct{}) bool {
	if fields == nil {
		return false
	}
	_, ok := fields[path]
	return ok
}
