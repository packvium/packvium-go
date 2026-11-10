package commerce

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// The versioned eligibility rule model: append-only per rule id, effective-dated, over a
// closed vocabulary of scopes and operators. A REJECT always outranks an ALLOW; within a
// pool the highest priority wins and ties break on the smallest rule id by code point.

// PolicyScope is one named eligibility domain.
type PolicyScope string

// PolicyOperator is one predicate comparison.
type PolicyOperator string

// PolicyAction is what a matching rule decides.
type PolicyAction string

// The closed vocabularies; anything outside them is refused at load time.
const (
	ScopeFacility    PolicyScope = "facility"
	ScopeCustomer    PolicyScope = "customer"
	ScopeCarrier     PolicyScope = "carrier"
	ScopeMaterial    PolicyScope = "material"
	ScopeHazmat      PolicyScope = "hazmat"
	ScopeTemperature PolicyScope = "temperature"
	ScopeService     PolicyScope = "service"

	OperatorEquals    PolicyOperator = "equals"
	OperatorNotEquals PolicyOperator = "not_equals"
	OperatorIn        PolicyOperator = "in"
	OperatorNotIn     PolicyOperator = "not_in"
	OperatorExists    PolicyOperator = "exists"
	OperatorAbsent    PolicyOperator = "absent"

	ActionAllow  PolicyAction = "allow"
	ActionReject PolicyAction = "reject"
)

var policyScopes = []string{
	string(ScopeFacility), string(ScopeCustomer), string(ScopeCarrier), string(ScopeMaterial),
	string(ScopeHazmat), string(ScopeTemperature), string(ScopeService),
}

var policyOperators = []string{
	string(OperatorEquals), string(OperatorNotEquals), string(OperatorIn),
	string(OperatorNotIn), string(OperatorExists), string(OperatorAbsent),
}

var policyActions = []string{string(ActionAllow), string(ActionReject)}

// PolicyPredicate is one condition `context[field] <operator> value`, or for exists and
// absent just whether field is present.
type PolicyPredicate struct {
	Scope    PolicyScope
	Field    string
	Operator PolicyOperator
	Value    interface{}
}

func newPolicyPredicate(scope PolicyScope, field string, operator PolicyOperator, value interface{}) (PolicyPredicate, error) {
	if field == "" {
		return PolicyPredicate{}, errors.New("field is required")
	}
	unary := operator == OperatorExists || operator == OperatorAbsent
	if !unary && value == nil {
		return PolicyPredicate{}, fmt.Errorf("operator %s requires a value", pythonRepr(string(operator)))
	}
	return PolicyPredicate{Scope: scope, Field: field, Operator: operator, Value: value}, nil
}

// matches can fail: comparing operands Python refuses to compare is a TypeError there,
// and silently answering false here would make Go the one language that admits the case.
func (predicate PolicyPredicate) matches(context *Object) (bool, error) {
	actual, present := context.Get(predicate.Field)
	switch predicate.Operator {
	case OperatorExists:
		return present, nil
	case OperatorAbsent:
		return !present, nil
	}
	if !present {
		return false, nil
	}
	switch predicate.Operator {
	case OperatorEquals:
		return pythonEqual(actual, predicate.Value), nil
	case OperatorNotEquals:
		return !pythonEqual(actual, predicate.Value), nil
	case OperatorIn:
		return pythonContains(predicate.Value, actual)
	}
	contained, err := pythonContains(predicate.Value, actual)
	return !contained, err
}

// PolicyRule is one immutable, numbered version of one rule id. All of its predicates
// must match for it to apply.
type PolicyRule struct {
	RuleID      string
	Version     int
	Scope       PolicyScope
	Action      PolicyAction
	Predicates  []PolicyPredicate
	Priority    *big.Int
	EffectiveAt *big.Int
	Reason      string
}

func (rule *PolicyRule) validate() error {
	switch {
	case rule.RuleID == "":
		return errors.New("rule_id is required")
	case len(rule.Predicates) == 0:
		return errors.New("a rule must have at least one predicate")
	case rule.EffectiveAt.Sign() < 0:
		return errors.New("effective_at cannot be negative")
	}
	for _, predicate := range rule.Predicates {
		if predicate.Scope != rule.Scope {
			return fmt.Errorf("every predicate of rule %s must share the rule's own scope", pythonRepr(rule.RuleID))
		}
	}
	return nil
}

// matches evaluates predicates in order and stops at the first miss, as all() does, so
// a TypeError surfaces exactly where the reference would raise it.
func (rule *PolicyRule) matches(context *Object) (bool, error) {
	for _, predicate := range rule.Predicates {
		matched, err := predicate.matches(context)
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}

// PolicyCitation names the rule version that produced a decision, and why.
type PolicyCitation struct {
	RuleID   string
	Version  int
	Action   PolicyAction
	Priority *big.Int
	Reason   string
}

// PolicyDecision is whether the context is admitted for the scope, and the citation
// explaining it -- nil when no rule matched (open by default).
type PolicyDecision struct {
	Scope    PolicyScope
	Allowed  bool
	Citation *PolicyCitation
}

// decide evaluates an already-resolved rule set. O(r * p) in rules and predicates.
func decide(rules []*PolicyRule, scope PolicyScope, context *Object) (PolicyDecision, error) {
	var rejects, allows []*PolicyRule
	for _, rule := range rules {
		if rule.Scope != scope {
			continue
		}
		matched, err := rule.matches(context)
		if err != nil {
			return PolicyDecision{}, err
		}
		if !matched {
			continue
		}
		if rule.Action == ActionReject {
			rejects = append(rejects, rule)
		} else {
			allows = append(allows, rule)
		}
	}
	pool := rejects
	if len(pool) == 0 {
		pool = allows
	}
	if len(pool) == 0 {
		return PolicyDecision{Scope: scope, Allowed: true}, nil
	}
	winner := pool[0]
	for _, rule := range pool[1:] {
		if outranks(rule, winner) {
			winner = rule
		}
	}
	return PolicyDecision{
		Scope:   scope,
		Allowed: winner.Action == ActionAllow,
		Citation: &PolicyCitation{
			RuleID: winner.RuleID, Version: winner.Version, Action: winner.Action,
			Priority: winner.Priority, Reason: winner.Reason,
		},
	}, nil
}

// outranks orders by (-priority, rule_id); Go's byte-wise string order on UTF-8 is the
// code-point order the reference sorts by.
func outranks(challenger, holder *PolicyRule) bool {
	if order := challenger.Priority.Cmp(holder.Priority); order != 0 {
		return order > 0
	}
	return challenger.RuleID < holder.RuleID
}

type ruleHistory struct {
	ruleID   string
	versions []*PolicyRule
}

// PolicyRegistry is the append-only history of policy rules, one history per rule id,
// kept in publication order.
type PolicyRegistry struct {
	histories []*ruleHistory
	byID      map[string]*ruleHistory
}

func newPolicyRegistry() *PolicyRegistry {
	return &PolicyRegistry{byID: map[string]*ruleHistory{}}
}

type ruleTerms struct {
	scope       PolicyScope
	action      PolicyAction
	predicates  []PolicyPredicate
	priority    *big.Int
	effectiveAt *big.Int
	reason      string
}

func (registry *PolicyRegistry) publish(ruleID string, terms ruleTerms) (*PolicyRule, error) {
	history := registry.byID[ruleID]
	if history == nil {
		history = &ruleHistory{ruleID: ruleID}
	}
	rule := &PolicyRule{
		RuleID:      ruleID,
		Version:     len(history.versions) + 1,
		Scope:       terms.scope,
		Action:      terms.action,
		Predicates:  terms.predicates,
		Priority:    terms.priority,
		EffectiveAt: terms.effectiveAt,
		Reason:      terms.reason,
	}
	if err := rule.validate(); err != nil {
		return nil, err
	}
	if registry.byID[ruleID] == nil {
		registry.byID[ruleID] = history
		registry.histories = append(registry.histories, history)
	}
	history.versions = append(history.versions, rule)
	return rule, nil
}

type ruleNotFound struct{ ruleID string }

func (err *ruleNotFound) Error() string {
	return "no rule registered under id " + pythonRepr(err.ruleID)
}

type ruleVersionNotFound struct {
	ruleID  string
	version *big.Int
}

func (err *ruleVersionNotFound) Error() string {
	return fmt.Sprintf("rule %s has no version %s", pythonRepr(err.ruleID), err.version)
}

func (registry *PolicyRegistry) version(ruleID string, number *big.Int) (*PolicyRule, error) {
	history := registry.byID[ruleID]
	if history == nil {
		return nil, &ruleNotFound{ruleID: ruleID}
	}
	for _, rule := range history.versions {
		if number.Cmp(big.NewInt(int64(rule.Version))) == 0 {
			return rule, nil
		}
	}
	return nil, &ruleVersionNotFound{ruleID: ruleID, version: number}
}

// rulePin is one [rule_id, version] pair of an explicit snapshot.
type rulePin struct {
	ruleID  string
	version *big.Int
}

// errDuplicatePin is a malformed snapshot, not a rejection.
var errDuplicatePin = errors.New("a policy snapshot cannot pin the same rule id twice")

// resolveVersions resolves an explicit snapshot in rule-id order, so the first missing
// pin reported is the same whatever order the caller listed them in. O(k log k + k h).
func (registry *PolicyRegistry) resolveVersions(pins []rulePin) ([]*PolicyRule, error) {
	seen := map[string]bool{}
	for _, pin := range pins {
		if seen[pin.ruleID] {
			return nil, errDuplicatePin
		}
		seen[pin.ruleID] = true
	}
	ordered := append([]rulePin(nil), pins...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ruleID < ordered[right].ruleID })
	rules := make([]*PolicyRule, 0, len(ordered))
	for _, pin := range ordered {
		rule, err := registry.version(pin.ruleID, pin.version)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// evaluate decides with, for every rule id, only the version effective at asOf.
// O(h + r * p) over all published versions h.
func (registry *PolicyRegistry) evaluate(scope PolicyScope, context *Object, asOf *big.Int) (PolicyDecision, error) {
	var effective []*PolicyRule
	for _, history := range registry.histories {
		rule := effectiveRule(history.versions, asOf)
		if rule != nil && rule.Scope == scope {
			effective = append(effective, rule)
		}
	}
	return decide(effective, scope, context)
}

func effectiveRule(versions []*PolicyRule, asOf *big.Int) *PolicyRule {
	var best *PolicyRule
	for _, candidate := range versions {
		if candidate.EffectiveAt.Cmp(asOf) > 0 {
			continue
		}
		if best == nil || candidate.EffectiveAt.Cmp(best.EffectiveAt) >= 0 {
			best = candidate
		}
	}
	return best
}
