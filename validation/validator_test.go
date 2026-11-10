package validation

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
)

// Expected issue lists were produced by the v1.0.0 reference engine.

const (
	tickPerMM = 16_000
	gram      = 8_000_000
)

func mm(l, w, h int64) domain.Dimensions {
	return domain.MustDimensions(l*tickPerMM, w*tickPerMM, h*tickPerMM)
}

func grams(value int64) *units.Weight {
	weight := units.MustWeight(value * gram)
	return &weight
}

func int64Pointer(value int64) *int64    { return &value }
func stringPointer(value string) *string { return &value }

func newItem(t *testing.T, id string, dimensions domain.Dimensions, configure func(*domain.ItemSpec)) *domain.Item {
	t.Helper()
	spec := domain.NewItemSpec(id, dimensions)
	configure(&spec)
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func newContainer(t *testing.T, spec domain.ContainerSpec) *domain.Container {
	t.Helper()
	container, err := domain.NewContainer(spec)
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func instance(item *domain.Item, sequence int64) domain.ItemInstance {
	return item.Instances()[sequence-1]
}

func at(item *domain.Item, sequence, x, y, z int64, rotation domain.Rotation) domain.Placement {
	return atWith(item, sequence, x, y, z, rotation, item.Dimensions().Rotated(rotation))
}

func atWith(item *domain.Item, sequence, x, y, z int64, rotation domain.Rotation, dimensions domain.Dimensions) domain.Placement {
	point := domain.MustPoint(x*tickPerMM, y*tickPerMM, z*tickPerMM)
	return domain.NewPlacement(instance(item, sequence), point, rotation, dimensions, point, dimensions, 1)
}

type fixture struct {
	request *domain.PackingRequest
	packed  []*domain.PackedContainer
	a       *domain.Item
	n1      *domain.Item
	c       *domain.Container
}

func newFixture(t *testing.T) fixture {
	a := newItem(t, "A", mm(50, 50, 50), func(s *domain.ItemSpec) {
		s.Weight, s.Quantity, s.Group = *grams(400), 3, stringPointer("g1")
		s.Tags, s.IncompatibleTags = []string{"x"}, []string{"y"}
	})
	b := newItem(t, "B", mm(50, 40, 30), func(s *domain.ItemSpec) {
		s.Weight, s.AllowedRotations, s.MustBeOnFloor = *grams(400), []domain.Rotation{domain.LWH}, true
		s.EligibleContainerTags, s.Tags = []string{"cold"}, []string{"y"}
	})
	n1 := newItem(t, "N1", mm(20, 20, 20), func(s *domain.ItemSpec) { s.Quantity, s.Group = 2, stringPointer("10") })
	n2 := newItem(t, "N2", mm(20, 20, 20), func(s *domain.ItemSpec) { s.Quantity, s.Group = 2, stringPointer("9") })
	z := newItem(t, "Z", mm(10, 10, 10), func(*domain.ItemSpec) {})
	c := newContainer(t, domain.ContainerSpec{ID: "C", InnerDimensions: mm(100, 100, 100), MaxPayload: grams(1000),
		Quantity: int64Pointer(1), Tags: []string{"dry"}, MaxItems: int64Pointer(2)})
	request, err := domain.NewPackingRequest([]*domain.Item{a, b, n1, n2}, []*domain.Container{c})
	if err != nil {
		t.Fatal(err)
	}
	packed := []*domain.PackedContainer{
		domain.NewPackedContainer(c, 1, []domain.Placement{
			at(a, 1, 0, 0, 0, domain.LWH), at(a, 2, 25, 0, 0, domain.LWH),
			atWith(b, 1, 50, 50, 10, domain.LHW, mm(50, 40, 30)),
			at(n1, 1, 0, 60, 0, domain.LWH), at(n2, 1, 0, 80, 0, domain.LWH)}),
		domain.NewPackedContainer(c, 2, []domain.Placement{
			at(a, 3, 0, 0, 0, domain.LWH), at(n1, 2, 90, 0, 0, domain.LWH),
			at(z, 1, 0, 60, 0, domain.LWH), at(a, 1, 0, 0, 50, domain.LWH)}),
	}
	return fixture{request, packed, a, n1, c}
}

var placementIssues = []string{
	"collision|A#1 with A#2",
	"incompatible_items|A#1: A is incompatible with B",
	"incompatible_items|A#2: A is incompatible with B",
	"forbidden_rotation|B#1",
	"dimension_mismatch|B#1",
	"must_be_on_floor|B#1: ",
	"container_ineligible|B#1",
	"incompatible_items|B#1: B is incompatible with A",
}

var containerIssueList = []string{
	"payload_exceeded|C#1",
	"max_items_exceeded|C#1",
	"outside_container|N1#2",
	"duplicate_item|A#1",
	"max_items_exceeded|C#2",
	"container_inventory_exceeded|C",
}

func rendered(report ValidationReport) []string {
	var out []string
	for _, issue := range report.Issues() {
		out = append(out, issue.Code+"|"+issue.Detail)
	}
	return out
}

func joined(parts ...[]string) []string {
	var out []string
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func assertIssues(t *testing.T, expected []string, report ValidationReport) {
	t.Helper()
	if !reflect.DeepEqual(expected, rendered(report)) {
		t.Fatalf("expected\n%q\ngot\n%q", expected, rendered(report))
	}
	if report.Valid() != (len(expected) == 0) {
		t.Fatalf("valid must mean no issues")
	}
}

func TestReportsEveryBrokenGuaranteeInReferenceOrder(t *testing.T) {
	f := newFixture(t)
	report := ValidateWithUnpacked(f.request, f.packed, 0.5, nil, []domain.UnpackedItem{domain.NewUnpackedItem(instance(f.a, 3), "", nil)})
	assertIssues(t, joined(placementIssues, []string{"insufficient_support|B#1: 0/512000000000 < 0.500000"}, containerIssueList, []string{
		"duplicate_item|A#3 is both packed and unpacked",
		"missing_reason|A#3",
		"missing_item|N2#2",
		"unknown_item|Z#1",
		"group_split|10: C#1, C#2",
		"group_split|g1: C#1, C#2",
		"group_partial|9: 1/2 packed",
	}), report)
}

func TestSkipsCompletenessWithoutAnUnpackedList(t *testing.T) {
	f := newFixture(t)
	assertIssues(t, joined(placementIssues, containerIssueList,
		[]string{"unknown_item|Z#1", "group_split|10: C#1, C#2", "group_split|g1: C#1, C#2"}),
		Validate(f.request, f.packed, 0, nil))
}

func TestAcceptsAValidSolution(t *testing.T) {
	f := newFixture(t)
	request, err := domain.NewPackingRequest([]*domain.Item{f.n1}, []*domain.Container{f.c})
	if err != nil {
		t.Fatal(err)
	}
	clean := domain.NewPackedContainer(f.c, 1, []domain.Placement{at(f.n1, 1, 0, 0, 0, domain.LWH), at(f.n1, 2, 20, 0, 0, domain.LWH)})
	assertIssues(t, nil, ValidateWithUnpacked(request, []*domain.PackedContainer{clean}, 0, nil, nil))
}
