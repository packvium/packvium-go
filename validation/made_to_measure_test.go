package validation

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/domain"
)

// A made-to-measure box is re-cut from the declared machine and the achieved extent; a box
// committed at the machine's maximum, or with a stale price, is caught.
func TestAMadeToMeasureBoxMustBeTheOneItsPlacementsCallFor(t *testing.T) {
	mmTicks := int64(tickPerMM)
	machine, err := domain.NewMadeToMeasure([3]int64{60 * mmTicks, 60 * mmTicks, 60 * mmTicks},
		[3]int64{25 * mmTicks, 25 * mmTicks, 25 * mmTicks}, 1000, 0, [3]int64{3 * mmTicks, 3 * mmTicks, 3 * mmTicks})
	if err != nil {
		t.Fatal(err)
	}
	declared := newContainer(t, domain.ContainerSpec{ID: "machine", CostMinor: 40, InnerDimensions: mm(1510, 1510, 1510),
		MadeToMeasure: machine})
	cube := newItem(t, "cube", mm(100, 100, 100), func(*domain.ItemSpec) {})
	request, err := domain.NewPackingRequest([]*domain.Item{cube}, []*domain.Container{declared})
	if err != nil {
		t.Fatal(err)
	}
	placements := []domain.Placement{at(cube, 1, 0, 0, 0, domain.LWH)}
	cut := declared.Fabricate([3]int64{100 * mmTicks, 100 * mmTicks, 100 * mmTicks})
	if report := Validate(request, []*domain.PackedContainer{domain.NewPackedContainer(cut, 1, placements)}, 0,
		nil); !report.Valid() {
		t.Fatalf("the right box is refused: %v", report.Issues())
	}
	report := Validate(request, []*domain.PackedContainer{domain.NewPackedContainer(declared, 1, placements)}, 0, nil)
	want := []ValidationIssue{{"fabricated_dimensions_mismatch", "machine#1"}, {"outer_dimensions_mismatch", "machine#1"},
		{"made_to_measure_cost_mismatch", "machine#1"}}
	if !reflect.DeepEqual(report.Issues(), want) {
		t.Fatalf("issues %v, want %v", report.Issues(), want)
	}
}

// A flat bag whose load breaks the girth rule is reported, though every box is inside it.
func TestABagLoadPastItsGirthIsReported(t *testing.T) {
	mailer := newContainer(t, domain.ContainerSpec{ID: "mailer", DeformableBoundary: domain.FlatBag,
		InnerDimensions: mm(240, 140, 100)})
	book := newItem(t, "book", mm(200, 100, 20), func(s *domain.ItemSpec) {
		s.Quantity, s.AllowedRotations = 3, []domain.Rotation{domain.LWH}
	})
	request, err := domain.NewPackingRequest([]*domain.Item{book}, []*domain.Container{mailer})
	if err != nil {
		t.Fatal(err)
	}
	placements := []domain.Placement{at(book, 1, 0, 0, 0, domain.LWH), at(book, 2, 0, 0, 20, domain.LWH),
		at(book, 3, 0, 0, 40, domain.LWH)}
	report := Validate(request, []*domain.PackedContainer{domain.NewPackedContainer(mailer, 1, placements)}, 0, nil)
	if want := []ValidationIssue{{"bag_girth_exceeded", "mailer#1"}}; !reflect.DeepEqual(report.Issues(), want) {
		t.Fatalf("issues %v, want %v", report.Issues(), want)
	}
}
