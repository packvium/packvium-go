package sequence

import (
	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
)

// VerifyLoadingPrefixBusinessRules reuses the load calculations proven for a finished scene
// against every loading prefix, not only the final state, and returns a *SequenceReplayError
// at the first step whose prefix breaks a limit: knowing which addition first broke it is
// more useful than "the finished scene is invalid". O(n) prefixes, each O(n^2) worst case.
//
// Ground contact rules are re-derived from the prefix exactly as the support constraint
// defines a supporting surface: at replay time the prefix is already the complete set.
func VerifyLoadingPrefixBusinessRules(placements []domain.Placement, order []int, container *domain.Container) error {
	if err := requirePermutation(order, len(placements)); err != nil {
		return err
	}
	densityLimit := constraint.DensityLimitTicks(container)
	present := make([]domain.Placement, 0, len(placements))
	for step, index := range order {
		present = append(present, placements[index])
		if problem := prefixProblem(present, densityLimit); problem != nil {
			return replayError(index, step, problem.Code+": "+problem.Detail)
		}
	}
	return nil
}

func prefixProblem(present []domain.Placement, densityLimit *int64) *constraint.Violation {
	units := constraint.Units(present)
	analysis := constraint.NewLoadAnalysis(units, constraint.NewLoadSupportGraph(units, 1))
	if problem := analysis.Overloaded(); problem != nil {
		return problem
	}
	if problem := analysis.StackLimitExceeded(); problem != nil {
		return problem
	}
	if problem := analysis.StackDensityExceeded(densityLimit); problem != nil {
		return problem
	}
	for index, count := range analysis.StackedCounts() {
		if !present[index].Instance().Item().Stackable() && count > 0 {
			return &constraint.Violation{Code: "non_stackable_item_has_load", Detail: units[index].Label}
		}
	}
	return groundContactProblem(present)
}

func groundContactProblem(present []domain.Placement) *constraint.Violation {
	candidate := present[len(present)-1]
	rule := candidate.Instance().Item().GroundContactRule()
	box := candidate.EnvelopeBox()
	if rule == nil || *rule == "free" || box.Origin().Z() == 0 {
		return nil
	}
	support := constraint.CandidateView(present[:len(present)-1], candidate.Instance(), box)
	count := len(support.Supporters())
	violated := (*rule == "covered" && !constraint.TouchesCorners(box, support.Surfaces())) ||
		(*rule == "single" && count != 1) || (*rule == "multiple" && count < 2)
	if violated {
		return &constraint.Violation{Code: "ground_contact_violation", Detail: candidate.Instance().ID()}
	}
	return nil
}
