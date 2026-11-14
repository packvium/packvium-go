package constraint

import (
	"sort"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// nestingEntry is the sort key of one nesting-capable unit: declared item type, depth, exact
// XY footprint, then height and index.
type nestingEntry struct {
	item                          string
	depth, x1, y1, x2, y2, z1, z2 int64
	index                         int
}

func (e nestingEntry) less(other nestingEntry) bool {
	if e.item != other.item {
		return e.item < other.item
	}
	left := [7]int64{e.depth, e.x1, e.y1, e.x2, e.y2, e.z1, e.z2}
	right := [7]int64{other.depth, other.x1, other.y1, other.x2, other.y2, other.z1, other.z2}
	for position := range left {
		if left[position] != right[position] {
			return left[position] < right[position]
		}
	}
	return e.index < other.index
}

func (e nestingEntry) sameColumn(other nestingEntry) bool {
	return e.item == other.item && e.depth == other.depth && e.x1 == other.x1 && e.y1 == other.y1 &&
		e.x2 == other.x2 && e.y2 == other.y2
}

// LoadSupportGraph is direct load support: ordinary face contact plus exact adjacent nesting
// contact.
//
// Nesting-capable units are sorted by declared item type, exact XY footprint and height, so
// only neighbours in one possible nested column are compared. Sorting is O(n log n) and
// rebuilding the existing contact edges is O(E), not an all-pairs scan. Once an adjacent
// nesting predecessor exists, every same-column face edge is replaced by that predecessor
// alone. Edge and child lists keep ascending unit-index order, preserving ContactGraph's
// remainder and traversal contract.
type LoadSupportGraph struct {
	face   *ContactGraph
	units  []LoadUnit
	nested bool
	// Only populated with nesting; without it this graph is the face graph.
	nestedSupporters [][]ContactEdge
	nestedChildren   [][]int
	// descending is the load order, memoised on first use. prior is the parent's order when
	// this graph is the parent plus one unit, so the order is one insertion away.
	descending []int
	prior      []int
}

// NewLoadSupportGraph builds the support graph over units; cellHint is as for
// NewContactGraph.
func NewLoadSupportGraph(units []LoadUnit, cellHint int64) *LoadSupportGraph {
	var nesting []nestingEntry
	boxes := make([]domain.Box, len(units))
	for index, unit := range units {
		boxes[index] = unit.Box
		if unit.NestingItemID == nil || unit.NestingHeightTicks == nil {
			continue
		}
		box := unit.Box
		nesting = append(nesting, nestingEntry{*unit.NestingItemID, *unit.NestingHeightTicks,
			box.Origin().X(), box.Origin().Y(), box.X2(), box.Y2(), box.Origin().Z(), box.Z2(), index})
	}
	graph := &LoadSupportGraph{face: NewContactGraph(boxes, cellHint), units: append([]LoadUnit(nil), units...)}
	if len(nesting) == 0 {
		return graph
	}
	graph.nested = true
	graph.nestedSupporters = nestedSupporters(graph.face, graph.units, nesting)
	graph.nestedChildren = childrenOf(graph.nestedSupporters)
	return graph
}

func nestedSupporters(face *ContactGraph, units []LoadUnit, nesting []nestingEntry) [][]ContactEdge {
	supporters := make([][]ContactEdge, len(units))
	for index := range units {
		supporters[index] = face.Supporters(index)
	}
	sort.Slice(nesting, func(a, b int) bool { return nesting[a].less(nesting[b]) })
	for position := 1; position < len(nesting); position++ {
		lower, upper := nesting[position-1], nesting[position]
		if !lower.sameColumn(upper) || lower.z1 >= upper.z1 {
			continue
		}
		lowerUnit, upperUnit := units[lower.index], units[upper.index]
		if lowerUnit.Box.Z2()-upperUnit.Box.Origin().Z() != *lowerUnit.NestingHeightTicks {
			continue
		}
		var retained []ContactEdge
		for _, edge := range supporters[upper.index] {
			if !inColumn(units[edge.Index], upper) {
				retained = append(retained, edge)
			}
		}
		insertAt := 0
		for insertAt < len(retained) && retained[insertAt].Index < lower.index {
			insertAt++
		}
		edge := ContactEdge{lower.index, lowerUnit.Box.OverlapAreaXY(upperUnit.Box)}
		retained = append(retained, ContactEdge{})
		copy(retained[insertAt+1:], retained[insertAt:])
		retained[insertAt] = edge
		supporters[upper.index] = retained
	}
	return supporters
}

func childrenOf(supporters [][]ContactEdge) [][]int {
	children := make([][]int, len(supporters))
	for child, edges := range supporters {
		for _, edge := range edges {
			children[edge.Index] = append(children[edge.Index], child)
		}
	}
	return children
}

func inColumn(supporter LoadUnit, upper nestingEntry) bool {
	box := supporter.Box
	return supporter.NestingItemID != nil && *supporter.NestingItemID == upper.item &&
		supporter.NestingHeightTicks != nil && *supporter.NestingHeightTicks == upper.depth &&
		box.Origin().X() == upper.x1 && box.Origin().Y() == upper.y1 && box.X2() == upper.x2 && box.Y2() == upper.y2
}

// WithUnit is this graph plus one more unit, appended at the next index.
//
// The search evaluates many candidates against one unchanged set of placements, and adding
// a box cannot change contact between two boxes already placed, so the face graph only needs
// its two planes queried. Nesting falls back to a full rebuild: a nesting predecessor
// replaces the face edges of everything in its column, so one new unit can rewrite edges
// arbitrarily far from itself and the delta is no longer local.
func (g *LoadSupportGraph) WithUnit(unit LoadUnit, cellHint int64) *LoadSupportGraph {
	extended := make([]LoadUnit, len(g.units), len(g.units)+1)
	copy(extended, g.units)
	extended = append(extended, unit)
	if g.nested || unit.NestingItemID != nil {
		return NewLoadSupportGraph(extended, cellHint)
	}
	// Without nesting this graph is the face graph, so its edges are read straight off it
	// rather than patched by a second implementation of the same rule.
	// Candidates evaluated against one state all extend the same parent, so its order is
	// settled once and each child inherits it.
	return &LoadSupportGraph{face: g.face.WithBox(unit.Box), units: extended, prior: g.descendingOrder()}
}

// descendingOrder is every unit index, highest top first, then highest bottom, then index:
// each unit comes after everything that can rest on it. O(n log n) once per graph, or, for a
// graph extended by one unit, a binary search and an O(n) copy of the parent's order. The
// slice is shared and must not be modified; not safe for concurrent use.
func (g *LoadSupportGraph) descendingOrder() []int {
	if g.descending != nil || len(g.units) == 0 {
		return g.descending
	}
	if g.prior == nil {
		g.descending = make([]int, len(g.units))
		for index := range g.descending {
			g.descending[index] = index
		}
		sort.Slice(g.descending, func(left, right int) bool {
			return g.loadsBefore(g.descending[left], g.descending[right])
		})
		return g.descending
	}
	added := len(g.units) - 1
	at := sort.Search(len(g.prior), func(position int) bool { return g.loadsBefore(added, g.prior[position]) })
	order := make([]int, 0, len(g.units))
	order = append(append(append(order, g.prior[:at]...), added), g.prior[at:]...)
	g.descending, g.prior = order, nil
	return g.descending
}

// loadsBefore is the load order's comparison; the index makes it total.
func (g *LoadSupportGraph) loadsBefore(left, right int) bool {
	l, r := g.units[left].Box, g.units[right].Box
	if l.Z2() != r.Z2() {
		return l.Z2() > r.Z2()
	}
	if l.Origin().Z() != r.Origin().Z() {
		return l.Origin().Z() > r.Origin().Z()
	}
	return left < right
}

// Supporters is what index directly bears on. The slice is shared and must not be modified.
func (g *LoadSupportGraph) Supporters(index int) []ContactEdge {
	if g.nested {
		return g.nestedSupporters[index]
	}
	return g.face.Supporters(index)
}

// Children is what bears directly on index. The slice is shared and must not be modified.
func (g *LoadSupportGraph) Children(index int) []int {
	if g.nested {
		return g.nestedChildren[index]
	}
	return g.face.Children(index)
}

// Len is the number of units in the graph.
func (g *LoadSupportGraph) Len() int { return len(g.units) }

// NonStackableFailure is the rule that a direct-support edge may never originate at a
// non-stackable item, checked for the unit at candidateIndex. placements are the placed
// units in graph index order. O(s + 1) for s supporters.
func (g *LoadSupportGraph) NonStackableFailure(placements []domain.Placement, item domain.ItemInstance,
	candidateIndex int) *Violation {
	for _, edge := range g.Supporters(candidateIndex) {
		supporter := placements[edge.Index].Instance().Item()
		if !supporter.Stackable() {
			return &Violation{"non_stackable", supporter.ID()}
		}
	}
	if !item.Item().Stackable() && len(g.Children(candidateIndex)) > 0 {
		return &Violation{"non_stackable", item.Item().ID()}
	}
	return nil
}

// DirectSupport is one positive-area direct supporter of a candidate.
type DirectSupport struct {
	Index int
	Box   domain.Box
	Area  int64
}

// DirectSupportView is a candidate's support surfaces (every box whose top meets its base)
// and its positive-area direct supporters, both in placement-index order.
type DirectSupportView struct {
	surfaces   []domain.Box
	supporters []DirectSupport
}

func (v DirectSupportView) Surfaces() []domain.Box { return append([]domain.Box(nil), v.surfaces...) }
func (v DirectSupportView) Supporters() []DirectSupport {
	return append([]DirectSupport(nil), v.supporters...)
}

// SupporterBoxes is the supporters' boxes, in placement-index order.
func (v DirectSupportView) SupporterBoxes() []domain.Box {
	boxes := make([]domain.Box, len(v.supporters))
	for position, support := range v.supporters {
		boxes[position] = support.Box
	}
	return boxes
}

// SupportingArea is the total contact area of the supporters.
func (v DirectSupportView) SupportingArea() int64 {
	total := int64(0)
	for _, support := range v.supporters {
		total = ints.AddExact(total, support.Area)
	}
	return total
}

type surfaceEntry struct {
	index int
	box   domain.Box
}

func sameNestingColumn(placement domain.Placement, item domain.ItemInstance, box domain.Box) bool {
	other := placement.EnvelopeBox()
	placed, candidate := placement.Instance().Item(), item.Item()
	placedDepth, candidateDepth := placed.NestingHeight(), candidate.NestingHeight()
	return placed.ID() == candidate.ID() && placedDepth != nil && candidateDepth != nil &&
		placedDepth.Ticks() == candidateDepth.Ticks() &&
		other.Origin().X() == box.Origin().X() && other.Origin().Y() == box.Origin().Y() &&
		other.X2() == box.X2() && other.Y2() == box.Y2()
}

// CandidateView is the direct support of one candidate box: O(m) time and O(m) output space
// for m placements.
//
// Ordinary face semantics are kept exactly. With an exact adjacent nesting predecessor, the
// shadowed same-column face surfaces are removed and that predecessor becomes one full-area
// supporter. Every list stays in placement-index order, so remainder and traversal decisions
// stay canonical.
func CandidateView(placements []domain.Placement, item domain.ItemInstance, box domain.Box) DirectSupportView {
	var surfaces []surfaceEntry
	var supporters []DirectSupport
	var nearestLower *surfaceEntry
	depth := item.Item().NestingHeight()
	for index, placement := range placements {
		other := placement.EnvelopeBox()
		if other.Z2() == box.Origin().Z() {
			surfaces = append(surfaces, surfaceEntry{index, other})
			if area := other.OverlapAreaXY(box); area > 0 {
				supporters = append(supporters, DirectSupport{index, other, area})
			}
		}
		if depth != nil && sameNestingColumn(placement, item, box) && other.Origin().Z() < box.Origin().Z() &&
			(nearestLower == nil || other.Origin().Z() > nearestLower.box.Origin().Z()) {
			nearestLower = &surfaceEntry{index, other}
		}
	}
	if nearestLower == nil || nearestLower.box.Z2()-box.Origin().Z() != depth.Ticks() {
		return newDirectSupportView(surfaces, supporters)
	}
	predecessor := *nearestLower
	keptSurfaces := surfaces[:0]
	for _, entry := range surfaces {
		if !sameNestingColumn(placements[entry.index], item, box) {
			keptSurfaces = append(keptSurfaces, entry)
		}
	}
	keptSupporters := supporters[:0]
	for _, support := range supporters {
		if !sameNestingColumn(placements[support.Index], item, box) {
			keptSupporters = append(keptSupporters, support)
		}
	}
	surfaceAt := 0
	for surfaceAt < len(keptSurfaces) && keptSurfaces[surfaceAt].index < predecessor.index {
		surfaceAt++
	}
	keptSurfaces = append(keptSurfaces, surfaceEntry{})
	copy(keptSurfaces[surfaceAt+1:], keptSurfaces[surfaceAt:])
	keptSurfaces[surfaceAt] = predecessor
	supporterAt := 0
	for supporterAt < len(keptSupporters) && keptSupporters[supporterAt].Index < predecessor.index {
		supporterAt++
	}
	keptSupporters = append(keptSupporters, DirectSupport{})
	copy(keptSupporters[supporterAt+1:], keptSupporters[supporterAt:])
	keptSupporters[supporterAt] = DirectSupport{predecessor.index, predecessor.box, predecessor.box.OverlapAreaXY(box)}
	return newDirectSupportView(keptSurfaces, keptSupporters)
}

func newDirectSupportView(surfaces []surfaceEntry, supporters []DirectSupport) DirectSupportView {
	boxes := make([]domain.Box, len(surfaces))
	for position, entry := range surfaces {
		boxes[position] = entry.box
	}
	return DirectSupportView{boxes, supporters}
}
