package domain

import (
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The worked numbers of docs/MADE-TO-MEASURE-CONTAINERS.md, as the reference's own tests pin
// them.

const tickMM = 16_000

func machineOf(t *testing.T, minimum, increment, costPerSquareMetre int64) *MadeToMeasure {
	t.Helper()
	machine, err := NewMadeToMeasure([3]int64{minimum * tickMM, minimum * tickMM, minimum * tickMM},
		[3]int64{increment * tickMM, increment * tickMM, increment * tickMM}, costPerSquareMetre, 0, [3]int64{})
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func cubeSides(side int64) [3]int64 { return [3]int64{side * tickMM, side * tickMM, side * tickMM} }

func TestGridRoundsUpOntoTheMachineGridAndNeverBelowTheMinimum(t *testing.T) {
	machine := machineOf(t, 60, 25, 0)
	for _, check := range []struct{ value, want int64 }{
		{200 * tickMM, 210 * tickMM}, {210 * tickMM, 210 * tickMM}, {1, 60 * tickMM},
	} {
		if got := machine.grid(0, check.value); got != check.want {
			t.Errorf("grid(%d) = %d, want %d", check.value, got, check.want)
		}
	}
	if got := machine.clamp(0, big.NewInt(2000*tickMM), 0, 1510*tickMM); got != 1510*tickMM {
		t.Errorf("clamp past the maximum = %d", got)
	}
	if got := machine.clamp(0, big.NewInt(30*tickMM), 0, 1510*tickMM); got != 60*tickMM {
		t.Errorf("clamp below the minimum = %d", got)
	}
}

func TestTheSplitCostEqualsTheDirectCeiling(t *testing.T) {
	generator := rand.New(rand.NewSource(2702))
	for round := 0; round < 200; round++ {
		rate := generator.Int63n(1 << 53)
		area := new(big.Int).Rand(generator, new(big.Int).Lsh(big.NewInt(1), 110))
		direct := new(big.Int).Mul(big.NewInt(rate), area)
		direct.Add(direct, squareMetreTicks2).Sub(direct, big.NewInt(1)).Quo(direct, squareMetreTicks2)
		if got := PerSquareMetre(rate, area); got.Cmp(direct) != 0 {
			t.Fatalf("rate %d area %s: %s, want %s", rate, area, got, direct)
		}
	}
	if PerSquareMetre(1, big.NewInt(1)).Int64() != 1 || PerSquareMetre(1000, big.NewInt(0)).Int64() != 0 {
		t.Fatal("a sliver of material does not round up to a whole minor unit")
	}
}

func TestTheWorkedBoxCostsItsFeePlusItsMaterial(t *testing.T) {
	if got := BoxCostMinorFor(1000, 40, cubeSides(210)); got.Int64() != 305 {
		t.Fatalf("cost %s, want 305", got)
	}
}

func TestTheIntegerCubeRootIsTheLeastCubeAtOrAbove(t *testing.T) {
	large := new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil)
	for _, check := range []struct{ value, root *big.Int }{
		{big.NewInt(0), big.NewInt(0)}, {big.NewInt(1), big.NewInt(1)}, {big.NewInt(7), big.NewInt(2)},
		{big.NewInt(8), big.NewInt(2)}, {big.NewInt(9), big.NewInt(3)}, {large, big.NewInt(10_000_000)},
		{new(big.Int).Add(large, big.NewInt(1)), big.NewInt(10_000_001)},
	} {
		if got := icbrtCeil(check.value); got.Cmp(check.root) != 0 {
			t.Errorf("icbrt(%s) = %s, want %s", check.value, got, check.root)
		}
	}
}

func TestAMachineOffItsOwnGridIsRefused(t *testing.T) {
	machine := machineOf(t, 60, 25, 0)
	for _, check := range []struct {
		inner [3]int64
		want  string
	}{{[3]int64{1500, 1510, 1510}, "does not divide"}, {[3]int64{50, 1510, 1510}, "exceeds inner_dimensions"}} {
		_, err := NewContainer(ContainerSpec{ID: "odd", MadeToMeasure: machine,
			InnerDimensions: MustDimensions(check.inner[0]*tickMM, check.inner[1]*tickMM, check.inner[2]*tickMM)})
		if err == nil || !strings.Contains(err.Error(), check.want) {
			t.Errorf("%v: %v, want %q", check.inner, err, check.want)
		}
	}
	for _, check := range []struct {
		minimum, increment, allowance [3]int64
		tare                          int64
		want                          string
	}{
		{[3]int64{0, 1, 1}, [3]int64{1, 1, 1}, [3]int64{}, 0, "minimum must be positive"},
		{[3]int64{1, 1, 1}, [3]int64{1, 0, 1}, [3]int64{}, 0, "increment must be positive"},
		{[3]int64{1, 1, 1}, [3]int64{1, 1, 1}, [3]int64{}, -1, "prices cannot be negative"},
		{[3]int64{1, 1, 1}, [3]int64{1, 1, 1}, [3]int64{0, -1, 0}, 0, "outer_allowance cannot be negative"},
	} {
		if _, err := NewMadeToMeasure(check.minimum, check.increment, 0, check.tare, check.allowance); err == nil ||
			!strings.Contains(err.Error(), check.want) {
			t.Errorf("%v, want %q", err, check.want)
		}
	}
}

func cubes(count int, side int64) [][3]int64 {
	edges := make([][3]int64, count)
	for index := range edges {
		edges[index] = cubeSides(side)
	}
	return edges
}

func TestEightCubesInTheWorkedMachineTry260310410Then1510(t *testing.T) {
	rungs := machineOf(t, 60, 25, 0).SolvingEnvelopes(cubeSides(1510), cubes(8, 100))
	if want := [][3]int64{cubeSides(260), cubeSides(310), cubeSides(410), cubeSides(1510)}; !reflect.DeepEqual(rungs, want) {
		t.Fatalf("rungs %v, want %v", rungs, want)
	}
}

func TestThreeCubesInTheSmallMachineTry190220290Then300(t *testing.T) {
	rungs := machineOf(t, 50, 10, 0).SolvingEnvelopes(cubeSides(300), cubes(3, 100))
	if want := [][3]int64{cubeSides(190), cubeSides(220), cubeSides(290), cubeSides(300)}; !reflect.DeepEqual(rungs, want) {
		t.Fatalf("rungs %v, want %v", rungs, want)
	}
}

func TestAPinnedCrossSectionKeepsOnlyTheMaximumRung(t *testing.T) {
	machine, err := NewMadeToMeasure([3]int64{200 * tickMM, 100 * tickMM, 100 * tickMM},
		[3]int64{70 * tickMM, 10 * tickMM, 10 * tickMM}, 0, 0, [3]int64{})
	if err != nil {
		t.Fatal(err)
	}
	maximum := [3]int64{1950 * tickMM, 100 * tickMM, 100 * tickMM}
	if rungs := machine.SolvingEnvelopes(maximum, cubes(5, 100)); !reflect.DeepEqual(rungs, [][3]int64{maximum}) {
		t.Fatalf("rungs %v", rungs)
	}
}

func TestRungsIgnoreTheOrderOfTheInstances(t *testing.T) {
	machine := machineOf(t, 60, 25, 0)
	edges := [][3]int64{{300 * tickMM, 20 * tickMM, 40 * tickMM}, cubeSides(100), {50 * tickMM, 60 * tickMM, 70 * tickMM}}
	reversed := [][3]int64{edges[2], edges[1], edges[0]}
	if !reflect.DeepEqual(machine.SolvingEnvelopes(cubeSides(1510), edges), machine.SolvingEnvelopes(cubeSides(1510), reversed)) {
		t.Fatal("the rungs depend on the instance order")
	}
}

func TestTheFabricatedBoxIsTheGriddedExtentWithItsPriceAndBoard(t *testing.T) {
	machine, err := NewMadeToMeasure(cubeSides(60), cubeSides(25), 1000, 0, cubeSides(3))
	if err != nil {
		t.Fatal(err)
	}
	declared, err := NewContainer(ContainerSpec{ID: "machine", CostMinor: 40, MadeToMeasure: machine,
		InnerDimensions: MustDimensions(1510*tickMM, 1510*tickMM, 1510*tickMM)})
	if err != nil {
		t.Fatal(err)
	}
	box := declared.Fabricate(cubeSides(200))
	if box.InnerDimensions() != MustDimensions(210*tickMM, 210*tickMM, 210*tickMM) ||
		*box.OuterDimensions() != MustDimensions(213*tickMM, 213*tickMM, 213*tickMM) || box.CostMinor() != 305 ||
		box.ID() != "machine" {
		t.Fatalf("box %v outer %v cost %d", box.InnerDimensions(), box.OuterDimensions(), box.CostMinor())
	}
}

func TestTheGirthRuleAdmitsEqualityAndRefusesOneTickMore(t *testing.T) {
	inner := MustDimensions(240*tickMM, 140*tickMM, 100*tickMM)
	if !GirthAdmits(inner, 200*tickMM, 100*tickMM, 40*tickMM) {
		t.Fatal("a package exactly on the girth is refused")
	}
	if GirthAdmits(inner, 200*tickMM, 100*tickMM, 40*tickMM+1) {
		t.Fatal("a package one tick past the girth is admitted")
	}
	if FitsFlatBagAlone(inner, MustDimensions(200*tickMM, 100*tickMM, 60*tickMM)) {
		t.Fatal("a book too thick for the bag fits it alone")
	}
}

func TestABagRefusesTheFieldsAnchoredToWalls(t *testing.T) {
	outer := MustDimensions(300*tickMM, 200*tickMM, 200*tickMM)
	_, err := NewContainer(ContainerSpec{ID: "mailer", DeformableBoundary: FlatBag, OuterDimensions: &outer,
		InnerDimensions: MustDimensions(240*tickMM, 140*tickMM, 100*tickMM)})
	if err == nil || err.Error() != "outer_dimensions cannot be combined with deformable_boundary" {
		t.Fatalf("got %v", err)
	}
	_, err = NewContainer(ContainerSpec{ID: "mailer", DeformableBoundary: "sack",
		InnerDimensions: MustDimensions(240*tickMM, 140*tickMM, 100*tickMM)})
	if err == nil || err.Error() != "unknown deformable_boundary kind 'sack'" {
		t.Fatalf("got %v", err)
	}
}
