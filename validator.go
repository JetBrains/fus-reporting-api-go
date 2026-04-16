package fus

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Validator implements the client-side event validation contract from the
// JVM SensitiveDataValidator. It rewrites an event so that only scheme-approved
// keys and values reach the wire, replacing unknown / non-matching pieces with
// validator sentinels. If the event's group is registered but its build/version
// falls outside the declared ranges, the event is dropped.
//
// Scope ported from JVM:
//   - event ID whitelist per group
//   - per-field rule set: enum, enum#ref, regexp, regexp#ref, range, range#ref
//   - build / version range filter per group
//   - sentinel replacement (third.party / validation.unmatched_rule /
//     validation.undefined_rule / validation.incorrect_rule)
//   - pass-through for FUS system events (registered, invoked, …)
//
// Not ported (unused by the TeamCity CLI spec):
//   - util#, expression, required:, default_value: rules
//   - dictionary rules beyond plain enum
//   - recursive validation of nested Map/List values in event_data
//   - system_data / client_data / ids validation pipelines
type Validator struct {
	groups   map[string]*compiledGroup
	globals  *globalRules
	allowAll bool // set only by NewPermissiveValidator
}

// NewPermissiveValidator returns a validator that accepts every event without
// any scheme check.
//
// WARNING: intended for SDK internal tests and early bring-up only. Using this
// in production defeats the client-side validation contract and will leak
// unvalidated payloads into the FUS pipeline. Prefer NewValidator(scheme).
func NewPermissiveValidator() *Validator {
	return &Validator{allowAll: true}
}

type compiledGroup struct {
	id             string
	buildRanges    []SchemeRange
	versionRanges  []SchemeRange
	eventIDRules   []rule            // rules applied to event.id
	eventDataRules map[string][]rule // rules applied to data[key] for each scheme-declared key
}

type globalRules struct {
	enums   map[string][]string
	regexps map[string]*regexp.Regexp
	ranges  map[string]IntRange
}

// Sentinel values returned by rule.validate. These line up with the JVM
// ValidationResultType enum. Only the ones we can actually produce are here.
type ruleResult int

const (
	resAccepted      ruleResult = iota
	resRejected                 // validation.unmatched_rule
	resIncorrectRule            // validation.incorrect_rule
	resUndefinedRule            // validation.undefined_rule
)

// description maps a ruleResult to the JVM sentinel string.
func (r ruleResult) description() string {
	switch r {
	case resAccepted:
		return "accepted"
	case resIncorrectRule:
		return "validation.incorrect_rule"
	case resUndefinedRule:
		return "validation.undefined_rule"
	default:
		return "validation.unmatched_rule"
	}
}

// rule is the closed interface for a single validation rule. The rule's input
// value is pre-escaped via EscapeEventIDOrFieldValue.
type rule interface {
	validate(value string) ruleResult
}

// enumRule accepts values that exactly equal one of the enum entries.
type enumRule struct{ values map[string]struct{} }

func (e enumRule) validate(v string) ruleResult {
	if len(e.values) == 0 {
		return resIncorrectRule
	}
	if _, ok := e.values[v]; ok {
		return resAccepted
	}
	return resRejected
}

// regexpRule accepts values that fully match the compiled pattern.
type regexpRule struct{ pat *regexp.Regexp }

func (r regexpRule) validate(v string) ruleResult {
	if r.pat == nil {
		return resIncorrectRule
	}
	if r.pat.MatchString(v) {
		return resAccepted
	}
	return resRejected
}

// rangeRule accepts integer values inside [from, to].
type rangeRule struct{ r IntRange }

func (rr rangeRule) validate(v string) ruleResult {
	n, err := strconv.Atoi(v)
	if err != nil {
		return resRejected
	}
	if n >= rr.r.From && n <= rr.r.To {
		return resAccepted
	}
	return resRejected
}

// incorrectRuleStub is returned when the rule expression itself couldn't be
// parsed (unknown prefix, broken regexp, missing reference). Its validate
// always returns INCORRECT_RULE.
type incorrectRuleStub struct{}

func (incorrectRuleStub) validate(string) ruleResult { return resIncorrectRule }

// fusSystemEvents mirrors EventLogSystemEvents.SYSTEM_EVENTS on the JVM. These
// event IDs bypass validation — they're metadata events emitted by the SDK
// (counter baseline "registered", state baseline "invoked", etc.).
var fusSystemEvents = map[string]struct{}{
	"registered":                       {},
	"invoked":                          {},
	"invocation.failed":                {},
	"validation.too_many_events":       {},
	"validation.too_many_events.alert": {},
}

// NewValidator compiles a Scheme into a ready-to-use validator. Rule expressions
// are parsed eagerly; a broken expression turns into an incorrectRuleStub so
// events still produce deterministic sentinels instead of crashing later.
func NewValidator(s *Scheme) (*Validator, error) {
	if s == nil {
		return nil, fmt.Errorf("fus: nil scheme")
	}
	g, err := compileGlobalRules(s.Rules)
	if err != nil {
		return nil, err
	}
	v := &Validator{
		groups:  make(map[string]*compiledGroup, len(s.Groups)),
		globals: g,
	}
	for _, gs := range s.Groups {
		cg := &compiledGroup{
			id:             gs.ID,
			buildRanges:    gs.Builds,
			versionRanges:  gs.Versions,
			eventDataRules: map[string][]rule{},
		}
		if gs.Rules != nil {
			// Merge group-local enums/regexps/ranges on top of globals,
			// mirroring JVM EventGroupRules.create + GlobalRulesHolder.
			merged := mergeRules(g, gs.Rules)
			for _, id := range gs.Rules.EventID {
				cg.eventIDRules = append(cg.eventIDRules, parseRuleExpr(id, merged))
			}
			for key, exprs := range gs.Rules.EventData {
				rules := make([]rule, 0, len(exprs))
				for _, e := range exprs {
					rules = append(rules, parseRuleExpr(e, merged))
				}
				if len(rules) > 0 {
					cg.eventDataRules[key] = rules
				}
			}
		}
		v.groups[gs.ID] = cg
	}
	return v, nil
}

func compileGlobalRules(r *SchemeRules) (*globalRules, error) {
	g := &globalRules{
		enums:   map[string][]string{},
		regexps: map[string]*regexp.Regexp{},
		ranges:  map[string]IntRange{},
	}
	if r == nil {
		return g, nil
	}
	for k, v := range r.Enums {
		g.enums[k] = v
	}
	for k, pat := range r.Regexps {
		compiled, err := regexp.Compile("^(?:" + pat + ")$")
		if err != nil {
			// Keep validator usable; references to a broken global ref become
			// incorrectRuleStub at lookup time. Mirrors JVM behavior.
			continue
		}
		g.regexps[k] = compiled
	}
	for k, rng := range r.Ranges {
		g.ranges[k] = rng
	}
	return g, nil
}

// mergeRules overlays group-local enums/regexps/ranges on top of the compiled
// globals so that enum#ref / regexp#ref inside a group can resolve both local
// and scheme-level references. Mirrors JVM EventGroupRules.create which
// receives both the GlobalRulesHolder and the group descriptor's own rules.
func mergeRules(base *globalRules, local *SchemeRules) *globalRules {
	if local == nil {
		return base
	}
	hasLocal := len(local.Enums) > 0 || len(local.Regexps) > 0 || len(local.Ranges) > 0
	if !hasLocal {
		return base
	}
	m := &globalRules{
		enums:   make(map[string][]string, len(base.enums)+len(local.Enums)),
		regexps: make(map[string]*regexp.Regexp, len(base.regexps)+len(local.Regexps)),
		ranges:  make(map[string]IntRange, len(base.ranges)+len(local.Ranges)),
	}
	for k, v := range base.enums {
		m.enums[k] = v
	}
	for k, v := range base.regexps {
		m.regexps[k] = v
	}
	for k, v := range base.ranges {
		m.ranges[k] = v
	}
	// Local overrides global on collision, matching JVM behavior.
	for k, v := range local.Enums {
		m.enums[k] = v
	}
	for k, pat := range local.Regexps {
		if compiled, err := regexp.Compile("^(?:" + pat + ")$"); err == nil {
			m.regexps[k] = compiled
		}
	}
	for k, v := range local.Ranges {
		m.ranges[k] = v
	}
	return m
}

// parseRuleExpr mirrors ValidationSimpleRuleFactory.createSimpleRule. Only the
// prefixes used by the TCX spec are recognized; everything else yields an
// incorrectRuleStub so the rule chain still produces a deterministic outcome.
func parseRuleExpr(expr string, g *globalRules) rule {
	s := strings.TrimSpace(expr)
	// Strip optional surrounding braces. {enum:...} / enum:... are equivalent.
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = s[1 : len(s)-1]
	}

	switch {
	case strings.HasPrefix(s, "enum:"):
		return makeEnumRule(strings.Split(s[len("enum:"):], "|"))
	case strings.HasPrefix(s, "enum#"):
		ref := s[len("enum#"):]
		vals, ok := g.enums[ref]
		if !ok {
			return incorrectRuleStub{}
		}
		return makeEnumRule(vals)
	case strings.HasPrefix(s, "regexp:"):
		compiled, err := regexp.Compile("^(?:" + s[len("regexp:"):] + ")$")
		if err != nil {
			return incorrectRuleStub{}
		}
		return regexpRule{pat: compiled}
	case strings.HasPrefix(s, "regexp#"):
		ref := s[len("regexp#"):]
		pat, ok := g.regexps[ref]
		if !ok {
			return incorrectRuleStub{}
		}
		return regexpRule{pat: pat}
	case strings.HasPrefix(s, "range:"):
		r, ok := parseIntRangeExpr(s[len("range:"):])
		if !ok {
			return incorrectRuleStub{}
		}
		return rangeRule{r: r}
	case strings.HasPrefix(s, "range#"):
		ref := s[len("range#"):]
		r, ok := g.ranges[ref]
		if !ok {
			return incorrectRuleStub{}
		}
		return rangeRule{r: r}
	default:
		return incorrectRuleStub{}
	}
}

func makeEnumRule(values []string) rule {
	if len(values) == 0 {
		return incorrectRuleStub{}
	}
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[strings.TrimSpace(v)] = struct{}{}
	}
	return enumRule{values: m}
}

// parseIntRangeExpr parses "5..10" style range literals.
func parseIntRangeExpr(s string) (IntRange, bool) {
	parts := strings.SplitN(s, "..", 2)
	if len(parts) != 2 {
		return IntRange{}, false
	}
	from, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	to, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return IntRange{}, false
	}
	return IntRange{From: from, To: to}, true
}

// Validate mirrors SensitiveDataValidator.validateEvent for LION v4. Returns
// (validated, false) when the event may be kept (possibly with sentinel
// replacement) and (zero, true) when the event must be dropped entirely —
// which only happens when the group is registered but its build/version filter
// rejects the event.
func (v *Validator) Validate(e LogEvent) (LogEvent, bool) {
	if v.allowAll {
		return e, false
	}
	g, known := v.groups[e.Group.ID]

	if known && !v.acceptsBuildVersion(g, e.Build, e.Group.Version) {
		return LogEvent{}, true
	}

	e.Event.ID = v.validateEventID(e.Event.ID, g, known)
	e.Event.Data = v.validateEventData(e.Event.Data, g, known)
	return e, false
}

func (v *Validator) acceptsBuildVersion(g *compiledGroup, build string, groupVersion int) bool {
	if len(g.buildRanges) > 0 && !acceptsIn(g.buildRanges, build, compareBuilds) {
		return false
	}
	if len(g.versionRanges) > 0 && !acceptsInVersion(g.versionRanges, groupVersion) {
		return false
	}
	return true
}

// acceptsIn returns true if value is inside at least one of the ranges using cmp.
// Range semantics mirror JVM: [from, to) — inclusive start, exclusive end.
// Empty From / To means unbounded on that side.
func acceptsIn(ranges []SchemeRange, value string, cmp func(a, b string) int) bool {
	for _, r := range ranges {
		if r.From != "" && cmp(r.From, value) > 0 {
			continue
		}
		if r.To != "" && cmp(r.To, value) <= 0 {
			continue
		}
		return true
	}
	return false
}

func acceptsInVersion(ranges []SchemeRange, version int) bool {
	for _, r := range ranges {
		if r.From != "" {
			if from, err := strconv.Atoi(r.From); err != nil || version < from {
				continue
			}
		}
		if r.To != "" {
			if to, err := strconv.Atoi(r.To); err != nil || version >= to {
				continue
			}
		}
		return true
	}
	return false
}

// compareBuilds does lexicographic dotted-version comparison. Good enough for
// semver-ish build strings ("0.1.0", "2024.12.1"); if something more elaborate
// is needed later, swap in a parser without changing acceptsIn.
func compareBuilds(a, b string) int {
	aa := strings.Split(a, ".")
	bb := strings.Split(b, ".")
	n := len(aa)
	if len(bb) > n {
		n = len(bb)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(aa) {
			ai, _ = strconv.Atoi(aa[i])
		}
		if i < len(bb) {
			bi, _ = strconv.Atoi(bb[i])
		}
		if ai != bi {
			return ai - bi
		}
	}
	return 0
}

func (v *Validator) validateEventID(eventID string, g *compiledGroup, groupKnown bool) string {
	if _, ok := fusSystemEvents[eventID]; ok {
		return eventID
	}
	if _, ok := validationResultTypes[eventID]; ok {
		return eventID
	}
	if !groupKnown || len(g.eventIDRules) == 0 {
		return resUndefinedRule.description()
	}
	escaped := EscapeEventIDOrFieldValue(eventID)
	if acceptRules(g.eventIDRules, escaped) == resAccepted {
		return eventID
	}
	return resRejected.description()
}

func (v *Validator) validateEventData(data map[string]any, g *compiledGroup, groupKnown bool) map[string]any {
	if !groupKnown {
		// JVM behavior: unknown group → single {undefined: undefined} marker.
		return map[string]any{resUndefinedRule.description(): resUndefinedRule.description()}
	}
	if data == nil {
		return nil
	}
	out := make(map[string]any, len(data))
	for key, raw := range data {
		rules, ok := g.eventDataRules[key]
		if !ok {
			// Field not in scheme: JVM stamps both key and value with the
			// UNDEFINED_RULE sentinel. Subsequent entries with an unknown key
			// collide on the same sentinel key, matching JVM HashMap behavior.
			out[resUndefinedRule.description()] = resUndefinedRule.description()
			continue
		}
		// Reserved sentinels pass through untouched.
		if s, ok := raw.(string); ok {
			if _, vt := validationResultTypes[s]; vt {
				out[key] = s
				continue
			}
			if _, se := fusSystemEvents[s]; se {
				out[key] = s
				continue
			}
		}
		str := EscapeEventIDOrFieldValue(valueToString(raw))
		switch acceptRules(rules, str) {
		case resAccepted:
			out[key] = raw
		case resIncorrectRule:
			out[key] = resIncorrectRule.description()
		default:
			out[key] = resRejected.description()
		}
	}
	return out
}

// acceptRules iterates the chain like JVM DataValidationRules.acceptRule: a
// rule result with isFinal=true stops the chain; otherwise the last seen
// result is returned. For the rule types we support, ACCEPTED and INCORRECT
// are final; REJECTED is not, so a later rule can still accept.
func acceptRules(rules []rule, value string) ruleResult {
	var last ruleResult = resRejected
	any := false
	for _, r := range rules {
		res := r.validate(value)
		if res == resAccepted || res == resIncorrectRule {
			return res
		}
		last = res
		any = true
	}
	if !any {
		return resRejected
	}
	return last
}

// valueToString renders a data-map value as a string for rule matching, matching
// JVM's toString() semantics on common types.
func valueToString(v any) string {
	switch vv := v.(type) {
	case nil:
		return ""
	case string:
		return vv
	case bool:
		if vv {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(vv)
	case int32:
		return strconv.FormatInt(int64(vv), 10)
	case int64:
		return strconv.FormatInt(vv, 10)
	case float64:
		// Integers passed via json.Unmarshal arrive as float64. Render without
		// a trailing ".0" when the value is integral.
		if vv == float64(int64(vv)) {
			return strconv.FormatInt(int64(vv), 10)
		}
		return strconv.FormatFloat(vv, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", vv)
	}
}
