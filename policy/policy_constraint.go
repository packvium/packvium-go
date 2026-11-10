package policy

import (
	"fmt"

	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/pyrepr"
)

// PolicyConstraint refuses a candidate placement that any participating rule forbids.
//
// O(m + r) per candidate for m placements already in the container and r rules: one pass
// collecting the tags present, then one pass over the rules, the same bound class as the
// compatibility and tag count checks the forms compile onto. Rules are in citation order, so
// the first refusal is already the one the contract cites.
type PolicyConstraint struct{ rules []PolicyRule }

// NewPolicyConstraint enforces rules, which must already be in citation order.
func NewPolicyConstraint(rules []PolicyRule) *PolicyConstraint {
	return &PolicyConstraint{append([]PolicyRule(nil), rules...)}
}

// Rules is the enforced rules in citation order.
func (c *PolicyConstraint) Rules() []PolicyRule { return append([]PolicyRule(nil), c.rules...) }

// Evaluate implements constraint.PlacementConstraint.
func (c *PolicyConstraint) Evaluate(context constraint.Context) constraint.Result {
	itemTags := context.Item().Item().Tags()
	containerTags := context.Container().Tags()
	var present map[string]int64
	for _, rule := range c.rules {
		if require, ok := rule.form.(RequireContainerTag); ok {
			if itemTags.Has(require.ItemTag) && !containerTags.Has(require.ContainerTag) {
				return reject(rule, "requires a container tagged "+pyrepr.String(require.ContainerTag))
			}
			continue
		}
		// Both remaining forms need what is already in the container, so the walk happens
		// once and only when such a rule exists. Only looked up, never iterated.
		if present == nil {
			present = tagCounts(context.Placements())
		}
		switch form := rule.form.(type) {
		case SeparateTags:
			if itemTags.Has(form.Tag) && present[form.FromTag] > 0 {
				return reject(rule, separation(form.Tag, form.FromTag))
			}
			if itemTags.Has(form.FromTag) && present[form.Tag] > 0 {
				return reject(rule, separation(form.FromTag, form.Tag))
			}
		case LimitTagPerContainer:
			if itemTags.Has(form.Tag) && present[form.Tag] >= form.MaxCount {
				return reject(rule, fmt.Sprintf("at most %d item(s) tagged %s per container", form.MaxCount, pyrepr.String(form.Tag)))
			}
		}
	}
	return constraint.Allow()
}

func separation(tag, other string) string {
	return pyrepr.String(tag) + " may not share a container with " + pyrepr.String(other)
}

// ProvesUnplaceable implements constraint.ProvenRejection.
//
// Only require_container_tag can be answered, and that is not a gap: it is a statement about
// the request alone, so it holds however the search goes. Segregation and per-container caps
// depend on what else was packed, and reporting those as proven would claim more than the
// engine knows. O(r * c) for r rules and c containers, once per unpacked item.
func (c *PolicyConstraint) ProvesUnplaceable(item domain.ItemInstance, containers []*domain.Container) *constraint.Violation {
	for _, rule := range c.rules {
		require, ok := rule.form.(RequireContainerTag)
		if !ok || !item.Item().Tags().Has(require.ItemTag) || offered(containers, require.ContainerTag) {
			continue
		}
		return &constraint.Violation{Code: "policy_rule", Detail: rule.Citation() + ": requires a container tagged " +
			pyrepr.String(require.ContainerTag) + ", which none of the containers offered carries"}
	}
	return nil
}

func offered(containers []*domain.Container, tag string) bool {
	for _, container := range containers {
		if container.Tags().Has(tag) {
			return true
		}
	}
	return false
}

func reject(rule PolicyRule, why string) constraint.Result {
	return constraint.Reject("policy_rule", rule.Citation()+": "+why)
}

func tagCounts(placements []domain.Placement) map[string]int64 {
	counts := make(map[string]int64)
	for _, placement := range placements {
		for _, tag := range placement.Instance().Item().Tags().Sorted() {
			counts[tag]++
		}
	}
	return counts
}
