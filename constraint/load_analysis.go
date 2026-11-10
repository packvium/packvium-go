package constraint

import (
	"math/big"
	"math/bits"
	"sort"

	"github.com/packvium/packvium-go/compression"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// SquareMetreTicks is one square metre in length ticks squared, the reference area a stack
// density limit is expressed per, derived from the tick scale so it can never drift from it.
const SquareMetreTicks = (1000 * units.TicksPerMM) * (1000 * units.TicksPerMM)

// LoadAnalysis is per-scene load analysis over an already constructed support graph.
//
// Search code that already owns an incremental LoadSupportGraph uses this session, so the
// graph and its derived load and ancestor vectors are each computed at most once. The load
// vector is O(n log n + E) once the graph exists; the ancestor sets are O(n^2) worst case, as
// the reference's are. Every query is O(n) and never rebuilds contact geometry. Not safe for
// concurrent use: the vectors are memoised on first use.
type LoadAnalysis struct {
	units        []LoadUnit
	graph        *LoadSupportGraph
	topLoads     []int64
	restingAbove [][]int
}

// NewLoadAnalysis analyses units over graph, which must have been built from the same units.
func NewLoadAnalysis(units []LoadUnit, graph *LoadSupportGraph) *LoadAnalysis {
	return &LoadAnalysis{units: units, graph: graph}
}

func analysisOf(units []LoadUnit) *LoadAnalysis {
	return NewLoadAnalysis(units, NewLoadSupportGraph(units, 1))
}

// TopLoads is the weight borne by each unit, propagated down the whole stack. Every box
// pushes its own weight plus everything resting on it onto its direct supports, split by
// contact area; the integer remainder goes to the last supporter so the total is conserved
// exactly.
func (a *LoadAnalysis) TopLoads() []int64 { return append([]int64(nil), a.loads()...) }

func (a *LoadAnalysis) loads() []int64 {
	if a.topLoads == nil {
		a.topLoads = a.computeTopLoads()
	}
	return a.topLoads
}

func (a *LoadAnalysis) computeTopLoads() []int64 {
	loads := make([]int64, len(a.units))
	order := make([]int, len(a.units))
	for index := range order {
		order[index] = index
	}
	// Highest top first, then highest bottom, then index: every unit is visited after
	// everything that can rest on it.
	sort.Slice(order, func(left, right int) bool {
		l, r := a.units[order[left]].Box, a.units[order[right]].Box
		if l.Z2() != r.Z2() {
			return l.Z2() > r.Z2()
		}
		if l.Origin().Z() != r.Origin().Z() {
			return l.Origin().Z() > r.Origin().Z()
		}
		return order[left] < order[right]
	})
	for _, upper := range order {
		supports := a.graph.Supporters(upper)
		totalArea := int64(0)
		for _, edge := range supports {
			totalArea = ints.AddExact(totalArea, edge.Area)
		}
		if totalArea == 0 {
			continue
		}
		downward := ints.AddExact(a.units[upper].WeightTicks, loads[upper])
		assigned := int64(0)
		for position, edge := range supports {
			share := downward - assigned
			if position != len(supports)-1 {
				share = mulDiv(downward, edge.Area, totalArea)
			}
			assigned = ints.AddExact(assigned, share)
			loads[edge.Index] = ints.AddExact(loads[edge.Index], share)
		}
	}
	return loads
}

// mulDiv is the exact floor(a * b / d) for non-negative a and b and a positive d. Contact
// areas are squared ticks and weights are fine-grained ticks, so their product routinely
// leaves int64.
func mulDiv(a, b, d int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	high, low := bits.Mul64(uint64(a), uint64(b))
	if high == 0 && low <= 1<<63-1 {
		return int64(low) / d
	}
	product := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return product.Quo(product, big.NewInt(d)).Int64()
}

// Overloaded is the first unit whose bearing limit is exceeded.
func (a *LoadAnalysis) Overloaded() *Violation {
	if !anyUnit(a.units, func(unit LoadUnit) bool { return unit.MaxTopLoadTicks != nil }) {
		return nil
	}
	loads := a.loads()
	for index, unit := range a.units {
		if unit.MaxTopLoadTicks != nil && loads[index] > *unit.MaxTopLoadTicks {
			return &Violation{"top_load_exceeded", unit.Label}
		}
	}
	return nil
}

// Crushed is the first compressible unit whose applied pressure exceeds its declared limit.
// max_top_load is a mass the box below must bear and the pressure limit is what the item
// itself must survive; an item can pass one and fail the other. A crush is a hard boundary,
// not a worse score.
func (a *LoadAnalysis) Crushed() *Violation {
	if !anyUnit(a.units, func(unit LoadUnit) bool { return unit.MaxCompressionPressureKPA != nil }) {
		return nil
	}
	loads := a.loads()
	for index, unit := range a.units {
		if unit.MaxCompressionPressureKPA == nil {
			continue
		}
		box := unit.Box
		area := new(big.Int).Mul(big.NewInt(box.X2()-box.Origin().X()), big.NewInt(box.Y2()-box.Origin().Y()))
		pressure, err := compression.AppliedPressure(units.MustWeight(loads[index]), area)
		if err != nil {
			// A box has positive sides by construction, so its footprint is positive.
			panic(err)
		}
		if pressure.ExceedsKPA(*unit.MaxCompressionPressureKPA) {
			return &Violation{"crush_violation", unit.Label}
		}
	}
	return nil
}

// RestingAbove is everything resting anywhere above each unit, following the support graph
// upward, each list in first-reached order: children first, each followed by its own
// ancestors depth first. The lists are shared and must not be modified.
func (a *LoadAnalysis) RestingAbove() [][]int {
	if a.restingAbove == nil {
		memo := make([][]int, len(a.units))
		done := make([]bool, len(a.units))
		for index := range a.units {
			a.aboveSet(index, memo, done)
		}
		a.restingAbove = memo
	}
	return a.restingAbove
}

func (a *LoadAnalysis) aboveSet(index int, memo [][]int, done []bool) []int {
	if done[index] {
		return memo[index]
	}
	var result []int
	seen := make(map[int]struct{})
	add := func(member int) {
		if _, found := seen[member]; !found {
			seen[member] = struct{}{}
			result = append(result, member)
		}
	}
	for _, child := range a.graph.Children(index) {
		add(child)
		for _, member := range a.aboveSet(child, memo, done) {
			add(member)
		}
	}
	memo[index], done[index] = result, true
	return result
}

// StackedCounts is how many items rest anywhere above each unit. A count, not a weight.
func (a *LoadAnalysis) StackedCounts() []int64 {
	above := a.RestingAbove()
	counts := make([]int64, len(above))
	for index, members := range above {
		counts[index] = int64(len(members))
	}
	return counts
}

// StackLimitExceeded is the first unit carrying more items above it than it allows.
func (a *LoadAnalysis) StackLimitExceeded() *Violation {
	if !anyUnit(a.units, func(unit LoadUnit) bool { return unit.MaxStackedItems != nil }) {
		return nil
	}
	counts := a.StackedCounts()
	for index, unit := range a.units {
		if unit.MaxStackedItems != nil && counts[index] > *unit.MaxStackedItems {
			return &Violation{"stacked_item_limit_exceeded", unit.Label}
		}
	}
	return nil
}

// StackDensityExceeded is the first unit whose cumulative load, its own weight included,
// crushes its own footprint. maxDensityTicks is weight ticks allowed per square metre of a
// unit's base area, checked at every level of the stack; nil means no limit. Compared by
// cross multiplication, never divided.
func (a *LoadAnalysis) StackDensityExceeded(maxDensityTicks *int64) *Violation {
	if maxDensityTicks == nil {
		return nil
	}
	squareMetre := big.NewInt(SquareMetreTicks)
	limit := big.NewInt(*maxDensityTicks)
	loads := a.loads()
	bearing, allowed := new(big.Int), new(big.Int)
	for index, unit := range a.units {
		bearing.SetInt64(ints.AddExact(unit.WeightTicks, loads[index])).Mul(bearing, squareMetre)
		allowed.SetInt64(unit.Box.Dimensions().BaseArea()).Mul(allowed, limit)
		if bearing.Cmp(allowed) > 0 {
			return &Violation{"stack_density_exceeded", unit.Label}
		}
	}
	return nil
}

// BearingFailure is the first of Overloaded, Crushed, StackLimitExceeded and
// StackDensityExceeded to fail, in that order.
func (a *LoadAnalysis) BearingFailure(maxDensityTicks *int64) *Violation {
	if failure := a.Overloaded(); failure != nil {
		return failure
	}
	if failure := a.Crushed(); failure != nil {
		return failure
	}
	if failure := a.StackLimitExceeded(); failure != nil {
		return failure
	}
	return a.StackDensityExceeded(maxDensityTicks)
}

// DensityLimitTicks is the container's stack density limit in weight ticks per square metre,
// or nil when it declares none.
func DensityLimitTicks(container *domain.Container) *int64 {
	limit := container.MaxStackDensity()
	if limit == nil {
		return nil
	}
	ticks := limit.Ticks()
	return &ticks
}

func anyUnit(units []LoadUnit, predicate func(LoadUnit) bool) bool {
	for _, unit := range units {
		if predicate(unit) {
			return true
		}
	}
	return false
}
