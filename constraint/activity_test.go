package constraint

import (
	"testing"

	"github.com/packvium/packvium-go/domain"
)

type customRule struct{}

func (customRule) Evaluate(Context) Result { return Allow() }

func activityFixture(t *testing.T, doors []string) (*domain.Container, domain.ItemInstance) {
	t.Helper()
	size := domain.MustDimensions(10, 10, 10)
	item, err := domain.NewItem(domain.NewItemSpec("a", size))
	if err != nil {
		t.Fatal(err)
	}
	container, err := domain.NewContainer(domain.ContainerSpec{ID: "c", InnerDimensions: size, AccessDirections: doors})
	if err != nil {
		t.Fatal(err)
	}
	return container, item.Instances()[0]
}

func activeCount(rules []PlacementConstraint, kind func(PlacementConstraint) bool) int {
	count := 0
	for _, rule := range rules {
		if kind(rule) {
			count++
		}
	}
	return count
}

func isStopAccessibility(rule PlacementConstraint) bool {
	_, ok := rule.(*StopAccessibilityConstraint)
	return ok
}

// A plain item in a plain container leaves only the support rule and the caller's own rule:
// every other built-in opts in on a flag this search does not set.
func TestActiveKeepsSupportAndCustomRules(t *testing.T) {
	container, item := activityFixture(t, nil)
	rules, err := Defaults(0, nil, customRule{})
	if err != nil {
		t.Fatal(err)
	}
	active := Active(rules, container, item, false, false)
	if len(active) != 2 {
		t.Fatalf("%d rules active, want support and the custom rule", len(active))
	}
	if _, ok := active[0].(*SupportConstraint); !ok {
		t.Fatalf("first active rule %T", active[0])
	}
	if _, ok := active[1].(customRule); !ok {
		t.Fatalf("second active rule %T", active[1])
	}
}

// The stop accessibility rule is live on a route when the container states doors, or when
// the configuration supplies a default for a container that states none.
func TestStopAccessibilityIsActiveOnlyWithDoors(t *testing.T) {
	withoutDoors, item := activityFixture(t, nil)
	withDoors, _ := activityFixture(t, []string{"+x"})
	plain, _ := Defaults(0, nil)
	defaulted, _ := Defaults(0, []string{"-x"})
	cases := []struct {
		rules     []PlacementConstraint
		container *domain.Container
		route     bool
		want      int
	}{
		{plain, withoutDoors, true, 0},
		{plain, withDoors, true, 1},
		{plain, withDoors, false, 0},
		{defaulted, withoutDoors, true, 1},
	}
	for index, c := range cases {
		if got := activeCount(Active(c.rules, c.container, item, false, c.route), isStopAccessibility); got != c.want {
			t.Errorf("case %d: %d stop accessibility rules active, want %d", index, got, c.want)
		}
	}
}
