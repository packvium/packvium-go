package constraint

import "github.com/packvium/packvium-go/domain"

// TopLoadConstraint refuses a placement that would rest on something unable to carry it.
//
// Bearing limits are checked against the cumulative load of the whole stack, not only the
// box directly underneath, so a tower of light items cannot crush its base.
//
// The support graph over the placed boxes is the same for every candidate evaluated against
// one search state, so one base per placement list is kept and each candidate is appended to
// it. The cache is a single entry: search evaluates a run of candidates against one state
// before moving on. Per candidate: O(n) to recognise the state and extend the graph, plus
// O(n log n + E) for the load vector. Mutable per-solve state, so one instance must not be
// shared between concurrent solves.
type TopLoadConstraint struct {
	placements []domain.Placement
	base       *LoadSupportGraph
	baseUnits  []LoadUnit
	hint       int64
}

// NewTopLoadConstraint is a constraint with an empty cache.
func NewTopLoadConstraint() *TopLoadConstraint { return &TopLoadConstraint{hint: 1} }

// baseFor is the support graph over current alone, rebuilt only when it cannot serve. The
// cell hint has to cover every candidate appended to the base and the widest is not known in
// advance, so it grows to fit the first candidate that needs it and the base is rebuilt that
// once. Sizing it from the container instead would always be safe and always coarse.
func (t *TopLoadConstraint) baseFor(current []domain.Placement, footprint int64) *LoadSupportGraph {
	same := t.base != nil && samePlacements(t.placements, current)
	if same && footprint <= t.hint {
		return t.base
	}
	if !same {
		t.hint = 1
	}
	if footprint > t.hint {
		t.hint = footprint
	}
	t.placements = append([]domain.Placement(nil), current...)
	t.baseUnits = Units(current)
	t.base = NewLoadSupportGraph(t.baseUnits, t.hint)
	return t.base
}

// samePlacements compares by value: equal placements yield an identical graph, and a private
// copy keeps the cache sound even if the caller reuses a slice's backing array.
func samePlacements(cached, current []domain.Placement) bool {
	if len(cached) != len(current) {
		return false
	}
	for index := range cached {
		if cached[index] != current[index] {
			return false
		}
	}
	return true
}

// Evaluate implements PlacementConstraint.
func (t *TopLoadConstraint) Evaluate(context Context) Result {
	if !context.StackSensitive() {
		return Allow()
	}
	unit := limitedUnit(context.EnvelopeBox(), context.Item())
	graph := t.baseFor(context.Placements(), footprint(unit.Box)).WithUnit(unit, t.hint)
	units := make([]LoadUnit, len(t.baseUnits), len(t.baseUnits)+1)
	copy(units, t.baseUnits)
	units = append(units, unit)
	if failure := graph.NonStackableFailure(context.Placements(), context.Item(), len(units)-1); failure != nil {
		return resultOf(failure)
	}
	analysis := NewLoadAnalysis(units, graph)
	failure := analysis.BearingFailure(DensityLimitTicks(context.Container()))
	return resultOf(failure)
}
