package constraint

import (
	"fmt"
	"strconv"

	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/ints"
)

// SupportScale is the fixed-point scale a support ratio is carried at. Ratios arrive as
// floats from the public API but never decide feasibility in floating point: each is
// converted once to a scaled integer and every comparison is exact.
const SupportScale = 1_000_000

// ScaledRatio is floor(ratio * SupportScale + 0.5). The explicit conversion keeps the product
// rounded on its own, as the reference computes it, rather than fused into the addition.
func ScaledRatio(ratio float64) int64 {
	return int64(float64(ratio*SupportScale) + 0.5)
}

// RequiredArea is the exact floor(baseArea * scaled / SupportScale) for a non-negative
// baseArea, without an overflowing product.
func RequiredArea(baseArea, scaled int64) int64 {
	return ints.AddExact(ints.MulExact(baseArea/SupportScale, scaled), ints.MulExact(baseArea%SupportScale, scaled)/SupportScale)
}

// SupportConstraint refuses a candidate resting on too little, on the wrong number of
// supporters for its ground contact rule, or with its centroid outside its contact hull.
// O(m) per candidate for the support view plus O(s log s) for the s supporters' hull.
type SupportConstraint struct{ globalMinimum float64 }

// NewSupportConstraint enforces at least globalMinimum on every item; an item may ask for
// more.
func NewSupportConstraint(globalMinimum float64) *SupportConstraint {
	return &SupportConstraint{globalMinimum}
}

// GlobalMinimum is the ratio every item is held to.
func (s *SupportConstraint) GlobalMinimum() float64 { return s.globalMinimum }

// CanReject reports whether this constraint can refuse some above-floor placement of item.
// Solver diagnostics ask every registered SupportConstraint, including caller-supplied ones
// with their own minimum, so an inactive default is skipped without ignoring an active one.
func (s *SupportConstraint) CanReject(item domain.ItemInstance) bool {
	return enforcesContactRule(item) || s.required(item) > 0
}

func (s *SupportConstraint) required(item domain.ItemInstance) float64 {
	return maxFloat(s.globalMinimum, item.Item().MinimumSupportRatio())
}

func enforcesContactRule(item domain.ItemInstance) bool {
	rule := item.Item().GroundContactRule()
	return rule != nil && *rule != "free"
}

// Evaluate implements PlacementConstraint.
func (s *SupportConstraint) Evaluate(context Context) Result {
	if context.Point().Z() == 0 || !s.CanReject(context.Item()) {
		return Allow()
	}
	box := context.EnvelopeBox()
	support := CandidateView(context.Placements(), context.Item(), box)
	supporters := support.SupporterBoxes()
	if failure := contactRuleFailure(context.Item(), box, support.surfaces, len(supporters)); failure != "" {
		return Reject("ground_contact_violation", failure)
	}
	required := s.required(context.Item())
	if required <= 0 {
		return Allow()
	}
	area := support.SupportingArea()
	baseArea := context.EnvelopeDimensions().BaseArea()
	if area < RequiredArea(baseArea, ScaledRatio(required)) {
		return Reject("insufficient_support", fmt.Sprintf("%d/%d < %s", area, baseArea, strconv.FormatFloat(required, 'f', 6, 64)))
	}
	// Area alone is not stability: a candidate can clear the ratio while overhanging its own
	// centre of gravity. Checked only once a minimum ratio is enforced, so a caller who never
	// asked for support checking sees no new rejection code.
	//
	// Above half the base the answer is already decided and the hull is not built. A
	// footprint is centrally symmetric, so every line through its centre bisects it; a
	// centroid outside the contact hull would put the whole contact region in one open
	// half-plane through that centre, and so under half the base.
	if ints.MulExact(area, 2) > baseArea {
		return Allow()
	}
	hull := ConvexHull(ContactHullPoints(box, supporters))
	if !PointInHull(DoubledCentroid(box), hull) {
		return Reject("centre_of_gravity_unsupported",
			fmt.Sprintf("%d/%d met but centroid outside the %d-point support hull", area, baseArea, len(hull)))
	}
	return Allow()
}

// contactRuleFailure is the refusal detail of an unmet ground contact rule, or "" when the
// rule holds or the item declares none.
func contactRuleFailure(item domain.ItemInstance, box domain.Box, surfaces []domain.Box, count int) string {
	if !enforcesContactRule(item) {
		return ""
	}
	switch *item.Item().GroundContactRule() {
	case "covered":
		if !TouchesCorners(box, surfaces) {
			return fmt.Sprintf("covered: %d supporter(s), not all four corners touched", count)
		}
	case "single":
		if count != 1 {
			return fmt.Sprintf("single: rests on %d item(s)", count)
		}
	case "multiple":
		if count < 2 {
			return fmt.Sprintf("multiple: rests on %d item(s)", count)
		}
	}
	return ""
}

// TouchesCorners reports whether every one of the box's four base corners rests on some
// surface. A ratio cannot express this: 90% of the footprint concentrated in the middle
// passes any lower ratio but leaves every corner hanging. O(4 * s).
func TouchesCorners(box domain.Box, surfaces []domain.Box) bool {
	corners := [4][2]int64{
		{box.Origin().X(), box.Origin().Y()}, {box.X2(), box.Origin().Y()},
		{box.Origin().X(), box.Y2()}, {box.X2(), box.Y2()},
	}
	for _, corner := range corners {
		touched := false
		for _, surface := range surfaces {
			if surface.Origin().X() <= corner[0] && corner[0] <= surface.X2() &&
				surface.Origin().Y() <= corner[1] && corner[1] <= surface.Y2() {
				touched = true
				break
			}
		}
		if !touched {
			return false
		}
	}
	return true
}

// maxFloat is the reference's max() over two non-NaN ratios: the first wins a tie.
func maxFloat(first, second float64) float64 {
	if second > first {
		return second
	}
	return first
}
