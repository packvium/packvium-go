package algorithm

import (
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/units"
)

// Expected sequences were produced by the PHP engine at tag v1.0.0.
func TestDrawsTheReferenceSequence(t *testing.T) {
	random := NewDeterministicRandom(42)
	var draws []int64
	for index := 0; index < 6; index++ {
		draws = append(draws, random.NextInt(7))
	}
	if want := []int64{0, 6, 1, 6, 5, 4}; !reflect.DeepEqual(draws, want) {
		t.Fatalf("draws %v, want %v", draws, want)
	}
}

func TestShufflesLikeTheReference(t *testing.T) {
	if got, want := NewDeterministicRandom(7).Permutation(10), []int{6, 1, 4, 9, 8, 2, 7, 3, 5, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shuffle %v, want %v", got, want)
	}
}

func TestSortKeyComparesExactlyAcrossRepresentations(t *testing.T) {
	huge, _ := new(big.Int).SetString("100000000000000000000", 10)
	cases := []struct {
		left, right SortKey
		want        int
	}{
		{Key(Int(1), Str("b")), Key(Int(1), Str("a")), 1},
		{Key(Int(1)), Key(Int(1), Int(0)), -1},
		{Key(Big(huge)), Key(Int(math.MaxInt64)), 1},
		{Key(Float(math.Inf(-1))), Key(Int(math.MinInt64)), -1},
		{Key(Float(-3)), Key(Int(-3)), 0},
		{Key(Int(9007199254740993)), Key(Float(9007199254740992)), 1},
		{Key(Str("é")), Key(Str("z")), 1},
		{Key(Bool(false)), Key(Bool(true)), -1},
	}
	for _, c := range cases {
		if got := c.left.Compare(c.right); got != c.want {
			t.Errorf("compare %v %v = %d, want %d", c.left, c.right, got, c.want)
		}
	}
}

func TestStableOrderKeepsTiesInInputOrder(t *testing.T) {
	keys := []int64{2, 1, 2, 1}
	if got := stableOrder(4, func(index int) SortKey { return Key(Int(keys[index])) }); !reflect.DeepEqual(got, []int{1, 3, 0, 2}) {
		t.Fatalf("order %v", got)
	}
}

func TestSpatialIndexReturnsEachBoundOnceInFirstSeenOrder(t *testing.T) {
	index := NewSpatialIndex(80, 80, 80)
	index.Add(0, Bound{0, 0, 0, 80, 80, 80})
	index.Add(1, Bound{70, 70, 70, 80, 80, 80})
	if got := index.Query(60, 60, 60, 80, 80, 80); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("query %v", got)
	}
	copied := index.Copy()
	copied.Add(2, Bound{0, 0, 0, 10, 10, 10})
	if got := index.Query(0, 0, 0, 5, 5, 5); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("copy shares cells: %v", got)
	}
}

func mm(value int64) int64 { return value * units.TicksPerMM }

func grams(value int64) units.Weight { return units.MustWeight(value * units.TicksPerG) }

func testItem(t *testing.T, id string, nesting bool) *domain.Item {
	t.Helper()
	spec := domain.NewItemSpec(id, domain.MustDimensions(mm(50), mm(50), mm(50)))
	spec.Weight, spec.Quantity = grams(400), 3
	if nesting {
		height := units.MustLength(mm(10))
		spec.NestingHeight = &height
	}
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func testContainers(t *testing.T) (*domain.Container, *domain.Container) {
	t.Helper()
	payload, quantity, maxItems := grams(1000), int64(1), int64(2)
	small, err := domain.NewContainer(domain.ContainerSpec{ID: "C", InnerDimensions: domain.MustDimensions(mm(100), mm(100), mm(100)),
		MaxPayload: &payload, Quantity: &quantity, MaxItems: &maxItems, CostMinor: 5, VoidFillReserveRatio: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	tall, err := domain.NewContainer(domain.ContainerSpec{ID: "D", InnerDimensions: domain.MustDimensions(mm(60), mm(60), mm(120)),
		CostMinor: 3})
	if err != nil {
		t.Fatal(err)
	}
	return small, tall
}

func bounds(t *testing.T, items []*domain.Item, containers []*domain.Container) []int64 {
	t.Helper()
	request, err := domain.NewPackingRequest(items, containers)
	if err != nil {
		t.Fatal(err)
	}
	return ComputeObjectiveBounds(request.Instances(), containers)
}

// Expected vectors and refusals were produced by the PHP engine at tag v1.0.0.
func TestComputesTheReferenceBoundVectors(t *testing.T) {
	cubes, nesting := testItem(t, "A", false), testItem(t, "N", true)
	small, tall := testContainers(t)
	cases := []struct {
		items      []*domain.Item
		containers []*domain.Container
		want       []int64
	}{
		{[]*domain.Item{cubes}, []*domain.Container{small}, []int64{1, 1, 5, 750000, 250000}},
		{[]*domain.Item{cubes}, []*domain.Container{small, tall}, []int64{0, 1, 3, 131944, 312500}},
		{[]*domain.Item{nesting}, []*domain.Container{small, tall}, []int64{0, 1, 3, 0, 0}},
		{[]*domain.Item{cubes, nesting}, []*domain.Container{tall}, []int64{0, 1, 3, 0, 0}},
	}
	for index, c := range cases {
		if got := bounds(t, c.items, c.containers); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: %v, want %v", index, got, c.want)
		}
	}
}

func TestEveryShapeThatShrinksIsFlagged(t *testing.T) {
	if !OccupiesLessThanItsBox(testItem(t, "N", true)) || OccupiesLessThanItsBox(testItem(t, "A", false)) {
		t.Fatal("nesting flag wrong")
	}
	spec := domain.NewItemSpec("P", domain.MustDimensions(mm(10), mm(10), mm(10)))
	ratio, pressure := int64(500000), int64(10)
	spec.ShapeType, spec.CompressionRatioPPM, spec.MaxCompressionPressureKPA = domain.Compressible, &ratio, &pressure
	compressible, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !OccupiesLessThanItsBox(compressible) {
		t.Fatal("compressible not flagged")
	}
}

func boundRefusal(t *testing.T, instances []NormalisedInstance, containers []NormalisedContainer) (message string) {
	t.Helper()
	var err error
	func() {
		defer RecoverRefusal(&err)
		BoundsFromNormalised(instances, containers)
	}()
	if _, ok := err.(*BoundOverflowError); !ok {
		t.Fatalf("want a bound overflow, got %v", err)
	}
	return err.Error()
}

func normalisedInstance(volume string) NormalisedInstance {
	value, _ := new(big.Int).SetString(volume, 10)
	return NormalisedInstance{value, 1, false}
}

func normalisedContainer(volume string, quantity *int64, cost int64) NormalisedContainer {
	value, _ := new(big.Int).SetString(volume, 10)
	return NormalisedContainer{value, value, 100, 10, nil, nil, quantity, cost}
}

func TestRefusesASumPastTheDeclaredCeiling(t *testing.T) {
	got := boundRefusal(t, []NormalisedInstance{normalisedInstance("2000000000000000000000000000000")},
		[]NormalisedContainer{normalisedContainer("1000", nil, 1)})
	want := "instance volume sums past the 1000000000000000000000000000000 ceiling the bound path declares;" +
		" refusing rather than returning a number no engine can agree on"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestRefusesAResultPastThePortableCeiling(t *testing.T) {
	two := int64(2)
	got := boundRefusal(t, []NormalisedInstance{normalisedInstance("1")},
		[]NormalisedContainer{normalisedContainer("1000", &two, math.MaxInt64)})
	if want := "opening cost bound is 9223372036854775807, above the 9007199254740991 exact portable result ceiling"; got != want {
		t.Fatalf("%q", got)
	}
}

func TestRefusesASaturatedContainerCount(t *testing.T) {
	instance := normalisedInstance("900000000000000000000000000")
	unit := NormalisedContainer{big.NewInt(1), big.NewInt(1), 100, 10, nil, nil, nil, 1}
	got := boundRefusal(t, []NormalisedInstance{instance, instance, instance}, []NormalisedContainer{unit})
	if want := "container count bound is 9223372036854775807, above the 9007199254740991 exact portable result ceiling"; got != want {
		t.Fatalf("%q", got)
	}
}
