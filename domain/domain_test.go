package domain

import (
	"math/big"
	"testing"

	"github.com/packvium/packvium-go/hull"
	"github.com/packvium/packvium-go/units"
)

// Expected messages were produced by the reference engine's models at v1.0.0, case by case.

func cube(t *testing.T) Dimensions {
	t.Helper()
	dimensions, err := DimensionsOf("100", "100", "100", "mm")
	if err != nil {
		t.Fatal(err)
	}
	return dimensions
}

func int64p(value int64) *int64 { return &value }

func TestItemRefusalsMatchReference(t *testing.T) {
	d := cube(t)
	text := "x"
	tooTall, short := units.MustLength(1_600_000), units.MustLength(160_000)
	unitTetra := []hull.Vertex{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	cases := []struct {
		edit func(*ItemSpec)
		want string
	}{
		{func(s *ItemSpec) { s.ID = "" }, "item id is required"},
		{func(s *ItemSpec) { s.Quantity = 0 }, "item quantity must be positive"},
		{func(s *ItemSpec) { s.MinimumSupportRatio = 1.5 }, "minimum_support_ratio must be between 0 and 1"},
		{func(s *ItemSpec) { s.MaxStackedItems = int64p(0) }, "max_stacked_items must be at least 1"},
		{func(s *ItemSpec) { s.StopIndex = int64p(1 << 53) }, "stop_index must be a non-negative safe integer"},
		{func(s *ItemSpec) { s.Value = int64p(-1) }, "value must be non-negative"},
		{func(s *ItemSpec) { s.NestingHeight = &tooTall }, "nesting_height must be at least zero and strictly less than the item's own height"},
		{func(s *ItemSpec) { s.GroundContactRule = &text }, "ground_contact_rule must be one of ['covered', 'free', 'multiple', 'single']"},
		{func(s *ItemSpec) { s.HullVertices = unitTetra }, "hull_vertices is not part of a rigid_cuboid item"},
		{func(s *ItemSpec) { s.ShapeType = ConvexHull }, "a convex_hull item requires hull_vertices"},
		{func(s *ItemSpec) {
			s.ShapeType, s.HullVertices = ConvexHull, []hull.Vertex{{0, 0, 0}, {1_600_001, 0, 0}, {0, 1, 0}, {0, 0, 1}}
		}, "hull_vertices span (1600001, 1, 1) does not fit inside dimensions (1600000, 1600000, 1600000)"},
		{func(s *ItemSpec) { s.ShapeType = Compressible }, "a compressible item requires both compression_ratio and max_compression_pressure_kpa"},
		{func(s *ItemSpec) {
			s.ShapeType, s.CompressionRatioPPM, s.MaxCompressionPressureKPA = Compressible, int64p(2_000_000), int64p(1)
		}, "compression_ratio must be between zero and one"},
		{func(s *ItemSpec) {
			s.ShapeType, s.CompressionRatioPPM, s.MaxCompressionPressureKPA = Compressible, int64p(1), int64p(-1)
		}, "max_compression_pressure_kpa cannot be negative"},
		{func(s *ItemSpec) { s.KeepUpright, s.AllowedRotations = true, []Rotation{LHW} }, "at least one rotation must be allowed"},
		{func(s *ItemSpec) {
			s.NestingHeight, s.ShapeType, s.CompressionRatioPPM, s.MaxCompressionPressureKPA = &short, Compressible, int64p(1), int64p(1)
		}, "nesting_height with shape_type compressible is not supported yet"},
	}
	for _, c := range cases {
		spec := NewItemSpec("a", d)
		c.edit(&spec)
		if _, err := NewItem(spec); err == nil || err.Error() != c.want {
			t.Errorf("got %v, want %q", err, c.want)
		}
	}
}

func TestContainerRefusalsMatchReference(t *testing.T) {
	d := cube(t)
	small, _ := DimensionsOf("50", "50", "50", "mm")
	sliver, _ := DimensionsOf("101", "1", "1", "mm")
	backwards := [2]Axle{NewAxle(units.MustLength(800_000), nil), NewAxle(units.MustLength(160_000), nil)}
	beyond := [2]Axle{NewAxle(units.MustLength(160_000), nil), NewAxle(units.MustLength(3_200_000), nil)}
	cases := []struct {
		edit func(*ContainerSpec)
		want string
	}{
		{func(s *ContainerSpec) { s.ID = "" }, "container id is required"},
		{func(s *ContainerSpec) { s.Quantity = int64p(0) }, "container quantity must be positive"},
		{func(s *ContainerSpec) { s.CostMinor = -1 }, "container cost cannot be negative"},
		{func(s *ContainerSpec) { s.VoidFillReserveRatio = 2 }, "void_fill_reserve_ratio must be between 0 and 1"},
		{func(s *ContainerSpec) { s.Axles = &backwards }, "the front axle must be strictly nearer the origin than the rear axle"},
		{func(s *ContainerSpec) { s.Axles = &beyond }, "axle positions must lie within the container's length"},
		{func(s *ContainerSpec) { s.TagLimits = map[string]int64{"x": 0} }, "tag_limits must be at least 1"},
		{func(s *ContainerSpec) { s.OuterDimensions = &small }, "outer dimensions cannot be smaller than inner dimensions"},
		{func(s *ContainerSpec) { s.Obstacles = []Obstacle{NewObstacle("o", NewBox(Point{}, sliver), nil)} }, "obstacle o lies outside container"},
	}
	for _, c := range cases {
		spec := ContainerSpec{ID: "c", InnerDimensions: d}
		c.edit(&spec)
		if _, err := NewContainer(spec); err == nil || err.Error() != c.want {
			t.Errorf("got %v, want %q", err, c.want)
		}
	}
}

func TestRateTableMatchesReference(t *testing.T) {
	refusals := []struct {
		brackets, prices []int64
		want             string
	}{
		{nil, nil, "rate_table requires at least one weight bracket"},
		{[]int64{1}, nil, "rate_table weight_brackets_g and prices_minor must be the same length"},
		{[]int64{0}, []int64{1}, "rate_table weight brackets must be positive"},
		{[]int64{2, 2}, []int64{1, 1}, "rate_table weight brackets must be strictly ascending"},
		{[]int64{1}, []int64{-1}, "rate_table prices cannot be negative"},
	}
	for _, c := range refusals {
		if _, err := NewRateTable(c.brackets, c.prices, 0, 0); err == nil || err.Error() != c.want {
			t.Errorf("got %v, want %q", err, c.want)
		}
	}
	table, err := NewRateTable([]int64{1000, 5000}, []int64{300, 200}, 250, 78)
	if err != nil {
		t.Fatal(err)
	}
	for weight, want := range map[int64]int64{0: 324, 1000: 324, 1001: 270, 5000: 270} {
		if got, ok := table.ChargeMinorOrNone(weight); !ok || got != want {
			t.Errorf("charge(%d) = %d, %t; want %d", weight, got, ok, want)
		}
	}
	if _, ok := table.ChargeMinorOrNone(5001); ok {
		t.Error("a weight above the last bracket was priced")
	}
}

func TestSpecsAreCopiedOnConstruction(t *testing.T) {
	spec := NewItemSpec("a", cube(t))
	spec.Tags = []string{"b", "a", "b"}
	item, err := NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Tags[0] = "changed"
	spec.AllowedRotations[0] = HWL
	if got := item.Tags().Sorted(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("tags %v", got)
	}
	if item.AllowedRotations()[0] != LWH {
		t.Error("a caller's slice reached into the item")
	}
}

func TestPackedContainerAccounting(t *testing.T) {
	inner, _ := DimensionsOf("200", "100", "100", "mm")
	container, err := NewContainer(ContainerSpec{ID: "box", InnerDimensions: inner, TareWeight: units.MustWeight(5)})
	if err != nil {
		t.Fatal(err)
	}
	spec := NewItemSpec("cube", cube(t))
	spec.Weight, spec.Quantity = units.MustWeight(8_000_000), 2
	item, _ := NewItem(spec)
	instances := item.Instances()
	side := cube(t)
	placements := []Placement{
		NewPlacement(instances[0], Point{}, LWH, side, Point{}, side, 1),
		NewPlacement(instances[1], Point{1_600_000, 0, 0}, LWH, side, Point{1_600_000, 0, 0}, side, 1),
	}
	packed := NewPackedContainer(container, 1, placements)
	if packed.ID() != "box#1" || packed.PayloadWeight().Ticks() != 16_000_000 || packed.GrossWeight().Ticks() != 16_000_005 {
		t.Errorf("weights %d %d", packed.PayloadWeight().Ticks(), packed.GrossWeight().Ticks())
	}
	if want := new(big.Int).Mul(big.NewInt(2), side.Volume()); packed.UsedVolume().Cmp(want) != 0 || packed.Utilization() != 1 {
		t.Errorf("volume %s utilization %v", packed.UsedVolume(), packed.Utilization())
	}
	if packed.CentreOfMassOffsetPPM() != 0 {
		t.Errorf("a symmetric load is off centre by %d ppm", packed.CentreOfMassOffsetPPM())
	}
	lopsided := NewPackedContainer(container, 1, placements[:1])
	if lopsided.CentreOfMassOffsetPPM() != 500_000 {
		t.Errorf("one cube in the left half is off centre by %d ppm, want 500000", lopsided.CentreOfMassOffsetPPM())
	}
}
