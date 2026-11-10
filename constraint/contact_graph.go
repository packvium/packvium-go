package constraint

import (
	"sort"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// ContactEdge is one direct support: the index of the box below and the contact area in
// square ticks.
type ContactEdge struct {
	Index int
	Area  int64
}

type levelEntry struct {
	index int
	box   domain.Box
}

type cellKey struct{ x, y int64 }

// levelIndex is a uniform spatial hash over one z-level's boxes, bucketed by XY cell so a
// query only scans the boxes near it instead of every other box at that level.
//
// cell must be at least as large as the largest footprint dimension of every box ever
// inserted into or queried against this index. Only then does a box span no more than a 2x2
// block of cells, which guarantees two overlapping boxes share at least one cell. Build is
// O(m); a query is O(k) in the boxes sharing its cells. Bucket order never reaches output:
// every caller sorts its matches by index.
type levelIndex struct {
	cell    int64
	buckets map[cellKey][]levelEntry
}

func newLevelIndex(cell int64, candidates []levelEntry) *levelIndex {
	index := &levelIndex{cell, make(map[cellKey][]levelEntry)}
	for _, entry := range candidates {
		for _, key := range index.cells(entry.box) {
			index.buckets[key] = append(index.buckets[key], entry)
		}
	}
	return index
}

func (l *levelIndex) cells(box domain.Box) []cellKey {
	x1, y1 := floorDiv(box.Origin().X(), l.cell), floorDiv(box.Origin().Y(), l.cell)
	x2, y2 := floorDiv(box.X2()-1, l.cell), floorDiv(box.Y2()-1, l.cell)
	keys := make([]cellKey, 0, 4)
	for _, key := range [4]cellKey{{x1, y1}, {x2, y1}, {x1, y2}, {x2, y2}} {
		if !containsCell(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

func containsCell(keys []cellKey, wanted cellKey) bool {
	for _, key := range keys {
		if key == wanted {
			return true
		}
	}
	return false
}

// near is every candidate that could overlap box in XY: a superset of the true overlaps,
// deduplicated by index. The caller still checks the overlap area itself.
func (l *levelIndex) near(box domain.Box) []levelEntry {
	var out []levelEntry
	seen := make(map[int]struct{})
	for _, key := range l.cells(box) {
		for _, entry := range l.buckets[key] {
			if _, found := seen[entry.index]; !found {
				seen[entry.index] = struct{}{}
				out = append(out, entry)
			}
		}
	}
	return out
}

// ContactGraph is direct top/bottom contact between boxes, derived from geometry alone.
//
// Built once per whole-container check and shared by every load-propagation computation, so
// no two of them can disagree on what "touches" means. Build is O(n + sum of per-level near
// candidates), O(n) for an evenly spread lattice; WithBox is O(n) for the outer copy plus
// O(k) for the two planes it queries. Edge lists are in ascending index order, which is
// contract: the load split hands its rounding remainder to the last edge.
//
// The per-plane indexes are built lazily and cached on the graph, so a graph must not be
// used from two goroutines at once. Slices returned by Supporters and Children are shared
// and must not be modified.
type ContactGraph struct {
	supporters    [][]ContactEdge
	children      [][]int
	boxes         []domain.Box
	cell          int64
	byTop         map[int64][]levelEntry
	byBottom      map[int64][]levelEntry
	topIndexes    map[int64]*levelIndex
	bottomIndexes map[int64]*levelIndex
}

// NewContactGraph builds the graph over boxes.
//
// cellHint is an upper bound on the footprint of any box later appended with WithBox.
// Without it the cell is sized from the boxes present now, and appending anything wider
// falls back to a full rebuild: correct, but in a search the candidate is a new item that may
// be the widest in the request. Too large a hint only makes each bucket coarser.
func NewContactGraph(boxes []domain.Box, cellHint int64) *ContactGraph {
	top := make(map[int64][]levelEntry)
	bottom := make(map[int64][]levelEntry)
	for index, box := range boxes {
		top[box.Z2()] = append(top[box.Z2()], levelEntry{index, box})
		bottom[box.Origin().Z()] = append(bottom[box.Origin().Z()], levelEntry{index, box})
	}
	// One global cell size rather than one per level: a querying box can be any size in
	// this scene, and a level index is only correct when its cell covers every box it will
	// ever index or be queried with.
	cell := ints.Max(1, cellHint)
	for _, box := range boxes {
		cell = ints.Max(cell, footprint(box))
	}
	supporters := make([][]ContactEdge, len(boxes))
	children := make([][]int, len(boxes))
	indexes := make(map[int64]*levelIndex)
	for index, box := range boxes {
		plane := box.Origin().Z()
		candidates, found := top[plane]
		if !found {
			continue
		}
		level, built := indexes[plane]
		if !built {
			level = newLevelIndex(cell, candidates)
			indexes[plane] = level
		}
		// The load split hands the rounding remainder to whichever edge is last, so edges
		// must land in ascending index order, not the spatial hash's cell order.
		for _, match := range overlapsOn(level, box, index) {
			supporters[index] = append(supporters[index], ContactEdge{match.Index, match.Area})
			children[match.Index] = append(children[match.Index], index)
		}
	}
	return &ContactGraph{
		supporters: supporters, children: children, boxes: append([]domain.Box(nil), boxes...), cell: cell,
		byTop: top, byBottom: bottom,
		// Only the top-plane indexes are populated by the build; the bottom-plane ones are
		// built on demand, because a from-scratch build never needs them.
		topIndexes: indexes, bottomIndexes: make(map[int64]*levelIndex),
	}
}

func footprint(box domain.Box) int64 {
	return ints.Max(box.X2()-box.Origin().X(), box.Y2()-box.Origin().Y())
}

// overlapsOn is every entry of level overlapping box with positive area, ascending by index,
// skipping the box's own index.
func overlapsOn(level *levelIndex, box domain.Box, self int) []ContactEdge {
	var found []ContactEdge
	for _, entry := range level.near(box) {
		if entry.index == self {
			continue
		}
		if area := entry.box.OverlapAreaXY(box); area > 0 {
			found = append(found, ContactEdge{entry.index, area})
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].Index < found[b].Index })
	return found
}

// WithBox is this graph plus one more box, appended at the next index.
//
// Adding a box cannot create or destroy contact between two boxes already here: contact is a
// pairwise geometric predicate. That is why a delta is sound, and why the base's edge lists
// are shared rather than recomputed. The result is identical to a fresh build over the
// extended list, not merely equivalent, because edge order is contract; the new box takes the
// highest index, so appending keeps every list ascending.
func (g *ContactGraph) WithBox(box domain.Box) *ContactGraph {
	index := len(g.boxes)
	if width := footprint(box); width > g.cell {
		// A larger box could step over cells in the middle of its own footprint and miss a
		// real overlap, so this is a correctness fallback, not an optimisation choice.
		return NewContactGraph(appendedBoxes(g.boxes, box), width)
	}
	// Both queries run before the copy so that a level index they build lands on this
	// graph first and the copy inherits it.
	below := g.matches(g.byTop, g.topIndexes, box.Origin().Z(), box)
	above := g.matches(g.byBottom, g.bottomIndexes, box.Z2(), box)

	supporters := make([][]ContactEdge, index, index+1)
	copy(supporters, g.supporters)
	children := make([][]int, index, index+1)
	copy(children, g.children)
	for _, match := range below {
		children[match.Index] = appendedInt(children[match.Index], index)
	}
	// Their supporter lists gain the new index, larger than every index already in them,
	// so ascending order is preserved by appending.
	var ownChildren []int
	for _, match := range above {
		ownChildren = append(ownChildren, match.Index)
		supporters[match.Index] = appendedEdge(supporters[match.Index], ContactEdge{index, match.Area})
	}
	supporters = append(supporters, below)
	children = append(children, ownChildren)

	// One box joins exactly two planes, so rewriting those two buckets is all that changed.
	entry := levelEntry{index, box}
	top := copyBuckets(g.byTop)
	top[box.Z2()] = appendedEntry(top[box.Z2()], entry)
	bottom := copyBuckets(g.byBottom)
	bottom[box.Origin().Z()] = appendedEntry(bottom[box.Origin().Z()], entry)
	// A level index is immutable once built, so every cached one may be shared, except on
	// the two planes whose bucket just gained a member.
	topIndexes := copyIndexes(g.topIndexes)
	delete(topIndexes, box.Z2())
	bottomIndexes := copyIndexes(g.bottomIndexes)
	delete(bottomIndexes, box.Origin().Z())
	return &ContactGraph{supporters, children, appendedBoxes(g.boxes, box), g.cell, top, bottom, topIndexes, bottomIndexes}
}

// matches is every box on plane overlapping box in XY, ascending by index.
func (g *ContactGraph) matches(buckets map[int64][]levelEntry, cache map[int64]*levelIndex, plane int64,
	box domain.Box) []ContactEdge {
	bucket, found := buckets[plane]
	if !found {
		return nil
	}
	level, built := cache[plane]
	if !built {
		level = newLevelIndex(g.cell, bucket)
		cache[plane] = level
	}
	return overlapsOn(level, box, -1)
}

// Supporters is what index directly rests on, each with the contact area.
func (g *ContactGraph) Supporters(index int) []ContactEdge { return g.supporters[index] }

// Children is what rests directly on index, not transitively.
func (g *ContactGraph) Children(index int) []int { return g.children[index] }

// Len is the number of boxes in the graph.
func (g *ContactGraph) Len() int { return len(g.boxes) }

// The appended* helpers always copy: a shared slice with spare capacity would otherwise be
// written through by the next append and corrupt the graph it came from.
func appendedBoxes(boxes []domain.Box, box domain.Box) []domain.Box {
	copied := make([]domain.Box, len(boxes), len(boxes)+1)
	copy(copied, boxes)
	return append(copied, box)
}

func appendedInt(values []int, value int) []int {
	copied := make([]int, len(values), len(values)+1)
	copy(copied, values)
	return append(copied, value)
}

func appendedEdge(edges []ContactEdge, edge ContactEdge) []ContactEdge {
	copied := make([]ContactEdge, len(edges), len(edges)+1)
	copy(copied, edges)
	return append(copied, edge)
}

func appendedEntry(entries []levelEntry, entry levelEntry) []levelEntry {
	copied := make([]levelEntry, len(entries), len(entries)+1)
	copy(copied, entries)
	return append(copied, entry)
}

func copyBuckets(source map[int64][]levelEntry) map[int64][]levelEntry {
	copied := make(map[int64][]levelEntry, len(source)+1)
	for plane, entries := range source {
		copied[plane] = entries
	}
	return copied
}

func copyIndexes(source map[int64]*levelIndex) map[int64]*levelIndex {
	copied := make(map[int64]*levelIndex, len(source))
	for plane, index := range source {
		copied[plane] = index
	}
	return copied
}

// floorDiv is floor(a / b) for a positive b, the reference's // operator.
func floorDiv(a, b int64) int64 {
	quotient := a / b
	if a%b != 0 && a < 0 {
		quotient--
	}
	return quotient
}
