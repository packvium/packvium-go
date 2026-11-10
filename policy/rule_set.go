package policy

import (
	"sort"
	"strings"

	"github.com/toxakara/packvium-go/constraint"
)

var policyKeys = []string{"as_of", "shipment", "rules"}

// PolicyRuleSet is the rules that participate in one request, already resolved and in
// citation order. Resolution is fixed by the contract and identical in every engine, or the
// same request would pack differently depending on which one answered it.
type PolicyRuleSet struct{ rules []PolicyRule }

// Rules is the participating rules in citation order.
func (s PolicyRuleSet) Rules() []PolicyRule { return append([]PolicyRule(nil), s.rules...) }

// ParseRuleSet reads the decoded policy value; nil declares no policy. O(r log r) for r
// rules.
func ParseRuleSet(value interface{}) (PolicyRuleSet, error) {
	if value == nil {
		return PolicyRuleSet{}, nil
	}
	raw, ok := object(value)
	if !ok {
		return PolicyRuleSet{}, refuse("policy must be an object")
	}
	if unknown := sortedUnknown(raw, policyKeys); len(unknown) > 0 {
		return PolicyRuleSet{}, refuse("policy has unknown keys: %s", strings.Join(unknown, ", "))
	}
	declared := raw["rules"]
	if falsy(declared) {
		return PolicyRuleSet{}, nil
	}
	list, ok := declared.([]interface{})
	if !ok {
		return PolicyRuleSet{}, refuse("policy.rules must be an array")
	}
	if _, present := raw["as_of"]; !present {
		// No default: a guessed instant silently activates or hides a restriction, and
		// reading a clock here would make one request pack differently on different days.
		return PolicyRuleSet{}, refuse("policy.as_of is required whenever policy.rules is non-empty")
	}
	asOf, err := integer(raw["as_of"], "policy.as_of", 0)
	if err != nil {
		return PolicyRuleSet{}, err
	}
	shipment, err := ParseShipmentContext(raw["shipment"], "policy.shipment")
	if err != nil {
		return PolicyRuleSet{}, err
	}
	parsed := make([]PolicyRule, len(list))
	for index, rule := range list {
		if parsed[index], err = ParseRule(rule, index); err != nil {
			return PolicyRuleSet{}, err
		}
	}
	return PolicyRuleSet{resolve(parsed, asOf, shipment)}, nil
}

func resolve(rules []PolicyRule, asOf int64, shipment ShipmentContext) []PolicyRule {
	// Append-only per id: among participating versions of one id the highest effective_at
	// wins, ties to the highest version, the same resolution catalog lookups use.
	latest := make(map[string]PolicyRule)
	for _, rule := range rules {
		if rule.effectiveAt > asOf || !rule.appliesTo.SatisfiedBy(shipment) {
			continue
		}
		current, found := latest[rule.id]
		if !found || isLater(rule, current) {
			latest[rule.id] = rule
		}
	}
	resolved := make([]PolicyRule, 0, len(latest))
	for _, rule := range latest {
		resolved = append(resolved, rule)
	}
	// Citation order: the first rule that rejects a candidate is the one cited. Ids are
	// unique here, so highest priority then smallest id is a total order and the map's
	// iteration order cannot leak.
	sort.Slice(resolved, func(a, b int) bool {
		if resolved[a].priority != resolved[b].priority {
			return resolved[a].priority > resolved[b].priority
		}
		return resolved[a].id < resolved[b].id
	})
	return resolved
}

func isLater(rule, current PolicyRule) bool {
	if rule.effectiveAt != current.effectiveAt {
		return rule.effectiveAt > current.effectiveAt
	}
	return rule.version > current.version
}

// Constraints is the rule set as pipeline constraints: none without rules, otherwise one
// *PolicyConstraint.
func (s PolicyRuleSet) Constraints() []constraint.PlacementConstraint {
	if len(s.rules) == 0 {
		return nil
	}
	return []constraint.PlacementConstraint{NewPolicyConstraint(s.rules)}
}
