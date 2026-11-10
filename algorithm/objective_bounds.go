package algorithm

import (
	"math"
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// Lower bounds on the objective vector. The mathematics is fixed by
// docs/OPTIMALITY-CERTIFICATES.md and held byte-identical to the reference on the shared
// corpus. Every volume, weight sum, capacity and selected cost is a *big.Int: a one-metre
// cube is 4.1e21 cubic ticks. O(n log n + c log c) time and O(n + c) space for n instances and
// c container types.

// MaxBoundSum is the declared ceiling every bound-path sum must stay below. Declared rather
// than inherited, so every engine refuses the same requests whatever its native integer.
var MaxBoundSum, _ = new(big.Int).SetString("1000000000000000000000000000000", 10)

// MaxBoundValue is the largest result integer every binding can carry unchanged.
var MaxBoundValue = big.NewInt(9007199254740991)

var (
	boundOne     = big.NewInt(1)
	boundLongMax = big.NewInt(math.MaxInt64)
)

// NormalisedInstance is one instance as the formulas consume it.
type NormalisedInstance struct {
	Volume  *big.Int
	Weight  int64
	Shrinks bool
}

// NormalisedContainer is one container type as the formulas consume it; nil is undeclared.
type NormalisedContainer struct {
	Usable    *big.Int
	Inner     *big.Int
	BaseArea  int64
	Height    int64
	Payload   *int64
	MaxItems  *int64
	Quantity  *int64
	CostMinor int64
}

func guardSum(total *big.Int, quantity string) *big.Int {
	if total.Cmp(MaxBoundSum) > 0 {
		refuse(&BoundOverflowError{quantity + " sums past the " + MaxBoundSum.String() +
			" ceiling the bound path declares; refusing rather than returning a number no engine can agree on"})
	}
	return total
}

func guardOutput(value *big.Int, quantity string) int64 {
	if value.Cmp(MaxBoundValue) > 0 {
		refuse(&BoundOverflowError{quantity + " bound is " + value.String() + ", above the " + MaxBoundValue.String() +
			" exact portable result ceiling"})
	}
	return value.Int64()
}

// ComputeObjectiveBounds is every bound for one request, in the default objective's key
// order. instances are quantity-expanded, exactly as the solver sees them.
func ComputeObjectiveBounds(instances []domain.ItemInstance, containers []*domain.Container) []int64 {
	normalisedInstances := make([]NormalisedInstance, len(instances))
	for index, instance := range instances {
		item := instance.Item()
		normalisedInstances[index] = NormalisedInstance{item.Dimensions().Volume(), item.Weight().Ticks(),
			OccupiesLessThanItsBox(item)}
	}
	normalisedContainers := make([]NormalisedContainer, len(containers))
	for index, container := range containers {
		inner := container.InnerDimensions()
		var payload *int64
		if limit := container.MaxPayload(); limit != nil {
			ticks := limit.Ticks()
			payload = &ticks
		}
		normalisedContainers[index] = NormalisedContainer{constraint.UsableVolume(container), inner.Volume(),
			inner.BaseArea(), inner.Height().Ticks(), payload, container.MaxItems(), container.Quantity(),
			container.CostMinor()}
	}
	return BoundsFromNormalised(normalisedInstances, normalisedContainers)
}

// BoundsFromNormalised is the same bounds from the numbers the formulas consume, the shape the
// shared corpus records.
func BoundsFromNormalised(instances []NormalisedInstance, containers []NormalisedContainer) []int64 {
	volumes := make([]*big.Int, len(instances))
	weights := make([]int64, len(instances))
	shrinks := false
	for index, instance := range instances {
		volumes[index], weights[index] = instance.Volume, instance.Weight
		shrinks = shrinks || instance.Shrinks
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Cmp(volumes[j]) < 0 })
	sort.Slice(weights, func(i, j int) bool { return weights[i] < weights[j] })
	count := int64(len(instances))
	// The a-priori check, once, on the way in: every later product is bounded by these totals
	// times PPM, so guarding them here makes the rest safe by derivation.
	guardSum(sumBig(volumes), "instance volume")
	guardSum(sumInt64s(weights), "instance weight")
	capacity, openingCost := new(big.Int), new(big.Int)
	for _, container := range containers {
		quantity := big.NewInt(1)
		if container.Quantity != nil {
			quantity = big.NewInt(*container.Quantity)
		}
		capacity.Add(capacity, new(big.Int).Mul(container.Usable, quantity))
		openingCost.Add(openingCost, new(big.Int).Mul(big.NewInt(container.CostMinor), quantity))
	}
	guardSum(capacity, "container capacity")
	guardSum(openingCost, "opening cost")
	unpacked := unpackedBound(volumes, weights, shrinks, count, containers)
	placed := count - unpacked
	opened := containerBound(volumes, weights, shrinks, placed, containers)
	cost := costBound(containers, opened)
	unused := unusedVolumeBound(volumes, shrinks, placed, containers, opened)
	height := stackHeightBound(volumes, shrinks, placed, containers, opened)
	return []int64{
		guardOutput(big.NewInt(unpacked), "unpacked count"),
		guardOutput(big.NewInt(opened), "container count"),
		guardOutput(cost, "opening cost"),
		guardOutput(unused, "unused volume"),
		guardOutput(height, "stack height"),
	}
}

// OccupiesLessThanItsBox reports whether an item can take up less room than its declared
// box: a nested item sinks into the one below, a hull leaves part of its box free, a
// compressible item gives up height under load. Each breaks the argument that nominal volumes
// sum to something a solution must carry.
func OccupiesLessThanItsBox(item *domain.Item) bool {
	if item.NestingHeight() != nil {
		return true
	}
	return item.ShapeType() == domain.ConvexHull || item.ShapeType() == domain.Compressible
}

// unpackedBound is L0: remove the geometry and ask what the declared resources alone forbid.
func unpackedBound(volumes []*big.Int, weights []int64, shrinks bool, count int64, containers []NormalisedContainer) int64 {
	placeable := count
	if !shrinks {
		placeable = ints.Min(placeable, fit(volumes, volumeCapacity(containers)))
	}
	payload := nativeCapacity(containers, func(container NormalisedContainer) *int64 { return container.Payload })
	placeable = ints.Min(placeable, fit(bigInts(weights), payload))
	if slots := nativeCapacity(containers, func(container NormalisedContainer) *int64 { return container.MaxItems }); slots != nil {
		if slots.Cmp(big.NewInt(count)) >= 0 {
			placeable = ints.Min(placeable, count)
		} else {
			placeable = ints.Min(placeable, slots.Int64())
		}
	}
	return count - placeable
}

// containerBound is L1: grant every container the largest capacity available, which can only
// understate how many are needed.
func containerBound(volumes []*big.Int, weights []int64, shrinks bool, placed int64, containers []NormalisedContainer) int64 {
	if placed <= 0 || len(containers) == 0 {
		return 0
	}
	bound := int64(1)
	if !shrinks {
		largest := new(big.Int)
		for _, container := range containers {
			if container.Usable.Cmp(largest) > 0 {
				largest = container.Usable
			}
		}
		if largest.Sign() > 0 {
			bound = ints.Max(bound, saturate(ceilDivBig(sumBig(volumes[:placed]), largest)))
		}
	}
	if payload := finiteMax(containers, func(container NormalisedContainer) *int64 { return container.Payload }); payload != nil && *payload > 0 {
		bound = ints.Max(bound, saturate(ceilDivBig(sumInt64s(weights[:placed]), big.NewInt(*payload))))
	}
	if slots := finiteMax(containers, func(container NormalisedContainer) *int64 { return container.MaxItems }); slots != nil && *slots > 0 {
		// (placed - 1) / slots + 1 is the ceiling without an intermediate that overflows for a
		// slot limit near the top of the range.
		bound = ints.Max(bound, (placed-1) / *slots + 1)
	}
	return bound
}

// costBound is L2: at least L1 containers open, each costing at least the cheapest the
// inventory still holds. Taking the cheapest types first with their multiplicity selects the
// same multiset as materialising every copy, in O(c log c).
func costBound(containers []NormalisedContainer, opened int64) *big.Int {
	if opened <= 0 {
		return new(big.Int)
	}
	cheapestFirst := append([]NormalisedContainer(nil), containers...)
	sort.SliceStable(cheapestFirst, func(i, j int) bool { return cheapestFirst[i].CostMinor < cheapestFirst[j].CostMinor })
	remaining := opened
	selected := new(big.Int)
	for _, container := range cheapestFirst {
		if remaining == 0 {
			break
		}
		repeat := opened
		if container.Quantity != nil {
			repeat = ints.Min(*container.Quantity, opened)
		}
		take := ints.Min(repeat, remaining)
		selected.Add(selected, new(big.Int).Mul(big.NewInt(container.CostMinor), big.NewInt(take)))
		remaining -= take
	}
	return guardSum(selected, "opening cost")
}

// unusedVolumeBound is L3: the fill is largest when every container is the smallest available
// and holds the greatest volume that could be placed at all.
func unusedVolumeBound(volumes []*big.Int, shrinks bool, placed int64, containers []NormalisedContainer, opened int64) *big.Int {
	if shrinks || opened <= 0 || len(containers) == 0 {
		return new(big.Int)
	}
	smallest := containers[0].Inner
	for _, container := range containers[1:] {
		if container.Inner.Cmp(smallest) < 0 {
			smallest = container.Inner
		}
	}
	if smallest.Sign() <= 0 {
		return new(big.Int)
	}
	largestPlaced := new(big.Int)
	if placed > 0 {
		largestPlaced = sumBig(volumes[int64(len(volumes))-placed:])
	}
	filled := ceilDivBig(new(big.Int).Mul(largestPlaced, scorePPM), smallest)
	bigOpened := big.NewInt(opened)
	maximum := new(big.Int).Mul(bigOpened, scorePPM)
	maximum.Sub(maximum, new(big.Int).Sub(bigOpened, boundOne))
	if filled.Cmp(maximum) >= 0 {
		return new(big.Int)
	}
	return maximum.Sub(maximum, filled)
}

// stackHeightBound is L4: the volume that must be placed stands at least as tall as itself
// spread across the widest floor available, in the tallest container available. The L1 - 1
// correction is the exact worst-case gap between a sum of floors and the floor of a sum.
func stackHeightBound(volumes []*big.Int, shrinks bool, placed int64, containers []NormalisedContainer, opened int64) *big.Int {
	if shrinks || opened <= 0 || len(containers) == 0 {
		return new(big.Int)
	}
	widest, tallest := int64(math.MinInt64), int64(math.MinInt64)
	for _, container := range containers {
		widest, tallest = ints.Max(widest, container.BaseArea), ints.Max(tallest, container.Height)
	}
	if widest <= 0 || tallest <= 0 {
		return new(big.Int)
	}
	required := new(big.Int)
	if placed > 0 {
		if total := sumBig(volumes[:placed]); total.Sign() > 0 {
			required = ceilDivBig(total, big.NewInt(widest))
		}
	}
	ratio := new(big.Int).Mul(required, scorePPM)
	ratio.Quo(ratio, big.NewInt(tallest))
	correction := big.NewInt(opened - 1)
	if ratio.Cmp(correction) <= 0 {
		return new(big.Int)
	}
	return ratio.Sub(ratio, correction)
}

// volumeCapacity is the sum of usable * quantity, or nil for an unbounded total. A container
// with no usable volume adds nothing however many there are.
func volumeCapacity(containers []NormalisedContainer) *big.Int {
	total := new(big.Int)
	for _, container := range containers {
		if container.Quantity == nil {
			if container.Usable.Sign() > 0 {
				return nil
			}
			continue
		}
		total.Add(total, new(big.Int).Mul(container.Usable, big.NewInt(*container.Quantity)))
	}
	return total
}

// nativeCapacity is the sum of limit * quantity, or nil when any limit or inventory is
// undeclared.
func nativeCapacity(containers []NormalisedContainer, limit func(NormalisedContainer) *int64) *big.Int {
	total := new(big.Int)
	for _, container := range containers {
		value := limit(container)
		if value == nil {
			return nil
		}
		if container.Quantity == nil {
			if *value > 0 {
				return nil
			}
			continue
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(*value), big.NewInt(*container.Quantity)))
		guardSum(total, "container capacity")
	}
	return total
}

// fit is the largest n such that the n smallest values sum to at most capacity; nil capacity
// is unbounded.
func fit(ascending []*big.Int, capacity *big.Int) int64 {
	if capacity == nil {
		return int64(len(ascending))
	}
	used := new(big.Int)
	for taken, value := range ascending {
		used.Add(used, value)
		if used.Cmp(capacity) > 0 {
			return int64(taken)
		}
	}
	return int64(len(ascending))
}

// finiteMax is the largest declared limit, or nil if any type declares none: one unlimited
// type makes every term conditioned on the maximum vacuous.
func finiteMax(containers []NormalisedContainer, limit func(NormalisedContainer) *int64) *int64 {
	var best *int64
	for _, container := range containers {
		value := limit(container)
		if value == nil {
			return nil
		}
		if best == nil || *value > *best {
			copied := *value
			best = &copied
		}
	}
	return best
}

// ceilDivBig is the ceiling of a non-negative quotient; zero stays zero.
func ceilDivBig(numerator, denominator *big.Int) *big.Int {
	if numerator.Sign() == 0 {
		return new(big.Int)
	}
	result := new(big.Int).Sub(numerator, boundOne)
	result.Add(result, denominator)
	return result.Quo(result, denominator)
}

// saturate clamps at int64's maximum, which the output guard then refuses with the same
// message every engine gives.
func saturate(value *big.Int) int64 {
	if value.Cmp(boundLongMax) > 0 {
		return math.MaxInt64
	}
	return value.Int64()
}

func sumBig(values []*big.Int) *big.Int {
	total := new(big.Int)
	for _, value := range values {
		total.Add(total, value)
	}
	return total
}

func sumInt64s(values []int64) *big.Int {
	total := new(big.Int)
	for _, value := range values {
		total.Add(total, big.NewInt(value))
	}
	return total
}

func bigInts(values []int64) []*big.Int {
	out := make([]*big.Int, len(values))
	for index, value := range values {
		out[index] = big.NewInt(value)
	}
	return out
}
