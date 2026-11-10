// Package policy holds versioned eligibility rules carried in the request and compiles them
// into the engine's own constraint pipeline. Rules travel as data because an engine is
// driven over JSON, so every engine must parse, resolve and cite them identically.
//
// Deliberately not a predicate language: tags already express the predicates, so each rule
// form compiles onto a check the pipeline already runs. A rule adds only what tags cannot
// carry: identity, effective dating, priority, and shipment-scoped facts.
package policy

import (
	"fmt"
	"strings"
)

// PolicyError is a rule set this engine cannot honour exactly as written. Structured rather
// than skipped: a rule dropped for being malformed would let a request pack in a way its own
// policy forbids.
type PolicyError struct{ message string }

func (e *PolicyError) Error() string { return e.message }

func refuse(format string, arguments ...interface{}) error {
	return &PolicyError{fmt.Sprintf(format, arguments...)}
}

// RuleForm is what a rule forbids: SeparateTags, RequireContainerTag or
// LimitTagPerContainer.
type RuleForm interface{ isRuleForm() }

// SeparateTags is that no container may hold both tags. It compiles to the compatibility
// check.
type SeparateTags struct{ Tag, FromTag string }

// RequireContainerTag is that an item carrying ItemTag may only enter a container carrying
// ContainerTag. It compiles to the container eligibility check.
type RequireContainerTag struct{ ItemTag, ContainerTag string }

// LimitTagPerContainer is that at most MaxCount items carrying Tag may share one container.
// It compiles to the tag count check.
type LimitTagPerContainer struct {
	Tag      string
	MaxCount int64
}

func (SeparateTags) isRuleForm()         {}
func (RequireContainerTag) isRuleForm()  {}
func (LimitTagPerContainer) isRuleForm() {}

// formKeys is each wire form name with its keys in constructor order, in declaration order.
// Exactly one may appear on a rule: a rule naming two forms has no single meaning for a
// citation.
var formKeys = []struct {
	name string
	keys []string
}{
	{"separate_tags", []string{"tag", "from_tag"}},
	{"require_container_tag", []string{"item_tag", "container_tag"}},
	{"limit_tag_per_container", []string{"tag", "max"}},
}

// sortedFormNames is the form names in code point order, as the refusal lists them.
const sortedFormNames = "limit_tag_per_container, require_container_tag, separate_tags"

// PolicyRule is one prohibition, with the identity, dating and priority tags cannot express.
type PolicyRule struct {
	id                             string
	version, effectiveAt, priority int64
	appliesTo                      ShipmentContext
	form                           RuleForm
}

// NewPolicyRule builds a rule from already validated parts.
func NewPolicyRule(id string, version, effectiveAt, priority int64, appliesTo ShipmentContext, form RuleForm) PolicyRule {
	return PolicyRule{id, version, effectiveAt, priority, appliesTo, form}
}

func (r PolicyRule) ID() string                 { return r.id }
func (r PolicyRule) Version() int64             { return r.version }
func (r PolicyRule) EffectiveAt() int64         { return r.effectiveAt }
func (r PolicyRule) Priority() int64            { return r.priority }
func (r PolicyRule) AppliesTo() ShipmentContext { return r.appliesTo }
func (r PolicyRule) Form() RuleForm             { return r.form }

// Citation is how a refusal names the rule: id@version.
func (r PolicyRule) Citation() string { return fmt.Sprintf("%s@%d", r.id, r.version) }

// ParseRule reads policy.rules[index]. O(k) in the rule's key count.
func ParseRule(value interface{}, index int) (PolicyRule, error) {
	where := fmt.Sprintf("policy.rules[%d]", index)
	raw, ok := object(value)
	if !ok {
		return PolicyRule{}, refuse("%s must be an object", where)
	}
	var named []int
	for position, form := range formKeys {
		if _, present := raw[form.name]; present {
			named = append(named, position)
		}
	}
	if len(named) != 1 {
		return PolicyRule{}, refuse("%s must name exactly one rule form (%s), not %d", where, sortedFormNames, len(named))
	}
	id, ok := nonEmptyString(raw["id"])
	if !ok {
		return PolicyRule{}, refuse("%s.id must be a non-empty string", where)
	}
	version, err := integer(raw["version"], where+".version", 1)
	if err != nil {
		return PolicyRule{}, err
	}
	effectiveAt, err := integer(raw["effective_at"], where+".effective_at", 0)
	if err != nil {
		return PolicyRule{}, err
	}
	priority, err := integer(raw["priority"], where+".priority", 0)
	if err != nil {
		return PolicyRule{}, err
	}
	appliesTo, err := ParseShipmentContext(raw["applies_to"], where+".applies_to")
	if err != nil {
		return PolicyRule{}, err
	}
	name := formKeys[named[0]].name
	form, err := parseForm(named[0], raw[name], where+"."+name)
	if err != nil {
		return PolicyRule{}, err
	}
	return PolicyRule{id, version, effectiveAt, priority, appliesTo, form}, nil
}

func parseForm(position int, value interface{}, where string) (RuleForm, error) {
	keys := formKeys[position].keys
	raw, ok := object(value)
	if !ok {
		return nil, refuse("%s must be an object", where)
	}
	if unknown := sortedUnknown(raw, keys); len(unknown) > 0 {
		return nil, refuse("%s has unknown keys: %s", where, strings.Join(unknown, ", "))
	}
	var missing []string
	for _, key := range keys {
		if _, present := raw[key]; !present {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, refuse("%s is missing %s", where, strings.Join(missing, ", "))
	}
	first, ok := nonEmptyString(raw[keys[0]])
	if !ok {
		return nil, refuse("%s.%s must be a non-empty string", where, keys[0])
	}
	if keys[1] == "max" {
		maximum, err := integer(raw["max"], where+".max", 0)
		if err != nil {
			return nil, err
		}
		return LimitTagPerContainer{first, maximum}, nil
	}
	second, ok := nonEmptyString(raw[keys[1]])
	if !ok {
		return nil, refuse("%s.%s must be a non-empty string", where, keys[1])
	}
	if position == 0 {
		return SeparateTags{first, second}, nil
	}
	return RequireContainerTag{first, second}, nil
}
