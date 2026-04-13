package fus

import "strings"

// Sentinel strings produced by the FUS scheme validator. A field name matching
// one of these is passed through unescaped so round-trips through server-side
// validation stay stable.
var validationResultTypes = map[string]struct{}{
	"accepted":                            {},
	"third.party":                         {},
	"validation.unmatched_rule":           {},
	"validation.incorrect_rule":           {},
	"validation.undefined_rule":           {},
	"validation.unreachable_metadata":     {},
	"validation.dictionary_not_found":     {},
	"validation.general_dictionary_error": {},
	"validation.unreachable.whitelist":    {},
	"validation.performance_issue":        {},
	"validation.required_field_missed":    {},
	"validation.default_value_applied":    {},
}

const (
	symbolsToReplaceIdentifier = ":;, "
	symbolsToReplaceFieldName  = ".:;, "
)

// Escape sanitizes a FUS identifier (group id, build, product, recorder id).
// Drops ' and ", replaces control chars / CR / LF / TAB / ":;, " with _, and
// non-ASCII runes with ?.
func Escape(s string) string {
	return escapeInternal(s, symbolsToReplaceIdentifier, false)
}

// EscapeEventIDOrFieldValue sanitizes an event ID or a string data value.
// Same as Escape but keeps ordinary spaces and CR / LF / TAB become spaces.
func EscapeEventIDOrFieldValue(s string) string {
	return escapeInternal(s, "", true)
}

// EscapeFieldName sanitizes a data-field or ids key. Same as Escape plus '.' -> '_'.
// Reserved validator sentinel strings pass through unchanged.
func EscapeFieldName(s string) string {
	if _, ok := validationResultTypes[s]; ok {
		return s
	}
	return escapeInternal(s, symbolsToReplaceFieldName, false)
}

// EscapeEventData returns a new map with every key escaped via EscapeFieldName
// and every string value escaped via EscapeEventIDOrFieldValue. Nested maps and
// slices are walked recursively; non-string scalars are passed through.
func EscapeEventData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[EscapeFieldName(k)] = escapeEventDataValue(v)
	}
	return out
}

// EscapeIDs returns a new map with keys escaped via EscapeFieldName and values
// escaped via EscapeEventIDOrFieldValue.
func EscapeIDs(ids map[string]string) map[string]string {
	if ids == nil {
		return nil
	}
	out := make(map[string]string, len(ids))
	for k, v := range ids {
		out[EscapeFieldName(k)] = EscapeEventIDOrFieldValue(v)
	}
	return out
}

func escapeEventDataValue(v any) any {
	switch vv := v.(type) {
	case string:
		return EscapeEventIDOrFieldValue(vv)
	case []any:
		out := make([]any, len(vv))
		for i, item := range vv {
			if item == nil {
				out[i] = nil
				continue
			}
			out[i] = escapeEventDataValue(item)
		}
		return out
	case map[string]any:
		return EscapeEventData(vv)
	default:
		return v
	}
}

func escapeInternal(s, toReplace string, allowSpaces bool) string {
	if !containsSystemSymbols(s, toReplace) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > 127 {
			b.WriteByte('?')
			continue
		}
		c := byte(r)
		switch {
		case isWhiteSpaceToReplace(c):
			if allowSpaces {
				b.WriteByte(' ')
			} else {
				b.WriteByte('_')
			}
		case isSymbolToReplace(c, toReplace):
			b.WriteByte('_')
		case isProhibitedSymbol(c):
			// dropped
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func containsSystemSymbols(s, toReplace string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
		c := byte(r)
		if isWhiteSpaceToReplace(c) || isSymbolToReplace(c, toReplace) || isProhibitedSymbol(c) {
			return true
		}
	}
	return false
}

func isWhiteSpaceToReplace(c byte) bool {
	return c == '\n' || c == '\r' || c == '\t'
}

func isSymbolToReplace(c byte, toReplace string) bool {
	if strings.IndexByte(toReplace, c) >= 0 {
		return true
	}
	return isASCIIControl(c)
}

func isASCIIControl(c byte) bool {
	return c < 32 || c == 127
}

func isProhibitedSymbol(c byte) bool {
	return c == '\'' || c == '"'
}
