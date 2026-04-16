package fus

// Anonymizer hashes event_data fields declared in the scheme's
// anonymized_fields using Anonymize(salt, value).
type Anonymizer struct {
	salt   []byte
	fields map[anonKey]map[string]struct{} // (groupID, eventID) → set of field names
}

type anonKey struct {
	group, event string
}

// NewAnonymizer builds an Anonymizer from the scheme's anonymized_fields.
func NewAnonymizer(s *Scheme, salt []byte) *Anonymizer {
	a := &Anonymizer{
		salt:   salt,
		fields: make(map[anonKey]map[string]struct{}),
	}
	if s == nil {
		return a
	}
	for _, g := range s.Groups {
		for _, af := range g.AnonymizedFields {
			if af.Event == "" || len(af.Fields) == 0 {
				continue
			}
			k := anonKey{group: g.ID, event: af.Event}
			fs := make(map[string]struct{}, len(af.Fields))
			for _, f := range af.Fields {
				fs[f] = struct{}{}
			}
			a.fields[k] = fs
		}
	}
	return a
}

// AnonymizeEvent hashes declared fields in e.Event.Data in place.
func (a *Anonymizer) AnonymizeEvent(e *LogEvent) {
	if a == nil || len(a.fields) == 0 {
		return
	}
	fs, ok := a.fields[anonKey{group: e.Group.ID, event: e.Event.ID}]
	if !ok || len(fs) == 0 {
		return
	}
	for k, v := range e.Event.Data {
		e.Event.Data[k] = anonymizeValue(a.salt, k, v, fs)
	}
}

func anonymizeValue(salt []byte, key string, v any, fields map[string]struct{}) any {
	switch vv := v.(type) {
	case string:
		if _, ok := fields[key]; ok {
			return Anonymize(salt, vv)
		}
		return vv
	case map[string]any:
		for mk, mv := range vv {
			nested := key + "." + mk
			vv[mk] = anonymizeValue(salt, nested, mv, fields)
		}
		return vv
	case []any:
		for i, item := range vv {
			if item != nil {
				vv[i] = anonymizeValue(salt, key, item, fields)
			}
		}
		return vv
	default:
		if _, ok := fields[key]; ok {
			return Anonymize(salt, valueToString(v))
		}
		return v
	}
}
