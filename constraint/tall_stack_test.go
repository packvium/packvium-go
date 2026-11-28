package constraint

import (
	"strconv"
	"testing"

	"github.com/packvium/packvium-go/domain"
)

// After the reference's tests/test_constraints.py at v1.7.0.

// A stack of 1,100 sheets one tick high is counted by an iterative walk, whose depth does not
// grow with the stack.
func TestAStackOfElevenHundredSheetsIsCounted(t *testing.T) {
	limit := int64(5_000)
	column := make([]LoadUnit, 1_100)
	for level := range column {
		column[level] = LoadUnit{Box: box(0, 0, int64(level), 10, 10, 1), MaxStackedItems: &limit,
			Label: "s" + strconv.Itoa(level)}
	}
	counts := StackedCounts(column)
	for level, count := range counts {
		if count != int64(1_099-level) {
			t.Fatalf("level %d carries %d", level, count)
		}
	}
	if violation := StackLimitExceeded(column); violation != nil {
		t.Fatal(violation)
	}
}

// left rests on base and carries high; right rests on base beside it. Both high and right
// block base, and the walk reaches high first through left: the lowest index is named.
func TestTheFirstBlockerNamedIsTheLowestIndex(t *testing.T) {
	scene := []LoadUnit{
		{Box: box(0, 0, 0, 20, 10, 10), Label: "base"}, {Box: box(0, 0, 10, 10, 10, 10), Label: "left"},
		{Box: box(10, 0, 10, 10, 10, 10), Label: "right"}, {Box: box(0, 0, 20, 10, 10, 10), Label: "high"},
	}
	violation := RouteOrderViolated(scene, []float64{0, 0, 1, 2})
	if violation == nil || violation.Code != "unloading_order_violation" || violation.Detail != "base blocked by right" {
		t.Fatalf("%+v", violation)
	}
}

func placedAt(t *testing.T, id string, x, y, z, l, w, h int64, rule *string) domain.Placement {
	t.Helper()
	spec := domain.NewItemSpec(id, domain.MustDimensions(l, w, h))
	spec.GroundContactRule = rule
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	origin := domain.MustPoint(x, y, z)
	return domain.NewPlacement(item.Instances()[0], origin, domain.LWH, item.Dimensions(), origin, item.Dimensions(), 1)
}

// a carries the front half of the candidate; b and c touch its two back corners with zero
// overlap. A corner touched only by a zero-area neighbour hangs, so covered refuses it.
func TestCoveredCountsOnlyBoxesTheCandidateRestsOn(t *testing.T) {
	covered := "covered"
	placements := []domain.Placement{placedAt(t, "a", 10, 0, 0, 10, 5, 10, nil),
		placedAt(t, "b", 0, 10, 0, 10, 10, 10, nil), placedAt(t, "c", 20, 10, 0, 10, 10, 10, nil)}
	candidate := placedAt(t, "top", 10, 0, 10, 10, 10, 10, &covered)
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "box", InnerDimensions: domain.MustDimensions(40, 40, 40)})
	if err != nil {
		t.Fatal(err)
	}
	context := NewContext(container, placements, candidate.Instance(), candidate.Position(), domain.LWH,
		candidate.Dimensions(), candidate.Dimensions())
	result := NewSupportConstraint(0).Evaluate(context)
	if result.Allowed() || result.Code() != "ground_contact_violation" ||
		result.Detail() != "covered: 1 supporter(s), not all four corners touched" {
		t.Fatalf("%+v", result)
	}
}
