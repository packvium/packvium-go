package commerce

import (
	"fmt"
	"math/big"
)

// The registries are built from a commerce document by LoadDocument. These constructors and
// publishers are for a caller that publishes into a registry of its own -- an approved
// recommendation, for one -- through the same append-only path the loader uses.

// NewCatalogRegistry is an empty publication history for one catalog.
func NewCatalogRegistry(catalogID string) (*CatalogRegistry, error) {
	return newCatalogRegistry(catalogID)
}

// Publish appends a new version and returns it. effectiveAt may be after publishedAt (a
// future-effective publication) or before it (a backdated correction); only the order of
// effective times across versions affects what resolves later. Neither may be negative.
func (registry *CatalogRegistry) Publish(snapshot *CatalogSnapshot, effectiveAt, publishedAt *big.Int,
	note string) (*CatalogVersion, error) {
	if effectiveAt == nil || publishedAt == nil {
		return nil, fmt.Errorf("effective_at and published_at are required")
	}
	if err := registry.publish(snapshot, effectiveAt, publishedAt, note); err != nil {
		return nil, err
	}
	return registry.versions[len(registry.versions)-1], nil
}

// NewPolicyRegistry is an empty policy rule history.
func NewPolicyRegistry() *PolicyRegistry { return newPolicyRegistry() }

// NewPolicyPredicate is one predicate over the closed scope and operator vocabularies; every
// operator but exists and absent needs a value.
func NewPolicyPredicate(scope PolicyScope, field string, operator PolicyOperator, value interface{}) (PolicyPredicate, error) {
	if !containsWord(policyScopes, string(scope)) {
		return PolicyPredicate{}, fmt.Errorf("unsupported policy scope %s", pythonRepr(string(scope)))
	}
	if !containsWord(policyOperators, string(operator)) {
		return PolicyPredicate{}, fmt.Errorf("unsupported policy operator %s", pythonRepr(string(operator)))
	}
	return newPolicyPredicate(scope, field, operator, value)
}

// PolicyRuleTerms is the content of one rule version; the registry numbers it.
type PolicyRuleTerms struct {
	Scope       PolicyScope
	Action      PolicyAction
	Predicates  []PolicyPredicate
	Priority    *big.Int
	EffectiveAt *big.Int
	Reason      string
}

// Publish appends a new, numbered version of ruleID and returns it. The vocabularies are
// checked again here, on top of NewPolicyPredicate, so a predicate assembled by hand rather
// than through it still fails admission.
func (registry *PolicyRegistry) Publish(ruleID string, terms PolicyRuleTerms) (*PolicyRule, error) {
	if terms.Priority == nil || terms.EffectiveAt == nil {
		return nil, fmt.Errorf("priority and effective_at are required")
	}
	if !containsWord(policyActions, string(terms.Action)) {
		return nil, fmt.Errorf("%s is not a valid PolicyAction", pythonRepr(string(terms.Action)))
	}
	for _, predicate := range terms.Predicates {
		if !containsWord(policyScopes, string(predicate.Scope)) || !containsWord(policyOperators, string(predicate.Operator)) {
			return nil, fmt.Errorf("rule %s uses an unsupported scope/operator combination", pythonRepr(ruleID))
		}
	}
	return registry.publish(ruleID, ruleTerms{
		scope: terms.Scope, action: terms.Action, predicates: append([]PolicyPredicate(nil), terms.Predicates...),
		priority: terms.Priority, effectiveAt: terms.EffectiveAt, reason: terms.Reason,
	})
}

func containsWord(words []string, wanted string) bool {
	for _, word := range words {
		if word == wanted {
			return true
		}
	}
	return false
}
