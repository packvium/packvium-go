package algorithm

import "github.com/packvium/packvium-go/internal/ints"

const cellsPerAxis int64 = 8

type cellKey struct{ x, y, z int64 }

// cellRange is a query's cells as [x1, x2, y1, y2, z1, z2), half open on every axis.
type cellRange [6]int64

// Bound is a solid's plain integer extent [x1, y1, z1, x2, y2, z2]; the candidate scan tests
// these millions of times.
type Bound [6]int64

// SpatialIndex is a uniform-grid broad phase for axis-aligned box collision queries. A cell
// assignment only needs to be a superset of the boxes a query could intersect: Query returns
// indices and the caller still runs the exact test on each, so an error here can only make the
// index slower, never admit a collision. A query costs O(cells touched + boxes in them)
// instead of O(all boxes).
//
// The reference also memoises answers by cell range. Here that cache hit about a third of the
// corpus's queries, saved no measurable time and allocated 6% more, so it is left out; the
// answers are the same either way.
type SpatialIndex struct {
	cellX, cellY, cellZ int64
	// cells holds buckets that are never mutated in place: Add replaces a bucket rather than
	// appending into its backing array, so a fork only has to copy the map and Query can hand
	// a bucket straight back.
	cells map[cellKey][]int
	// seen deduplicates one query's results without allocating a set per query; it is scratch
	// space, never shared between copies.
	seen  []uint32
	stamp uint32
}

// NewSpatialIndex sizes the grid for a container's inner extent.
func NewSpatialIndex(lengthTicks, widthTicks, heightTicks int64) *SpatialIndex {
	return &SpatialIndex{
		cellX: ints.Max(1, ceilDiv(ints.Max(1, lengthTicks), cellsPerAxis)),
		cellY: ints.Max(1, ceilDiv(ints.Max(1, widthTicks), cellsPerAxis)),
		cellZ: ints.Max(1, ceilDiv(ints.Max(1, heightTicks), cellsPerAxis)),
		cells: map[cellKey][]int{},
	}
}

// Copy is a structural fork, independent of the original from here on. Buckets are
// immutable, so copying the map is a complete fork: O(cells), not O(entries).
func (s *SpatialIndex) Copy() *SpatialIndex {
	cells := make(map[cellKey][]int, len(s.cells))
	for key, indices := range s.cells {
		cells[key] = indices
	}
	return &SpatialIndex{cellX: s.cellX, cellY: s.cellY, cellZ: s.cellZ, cells: cells}
}

// Add files a bound under every cell it touches.
func (s *SpatialIndex) Add(index int, bound Bound) {
	cells := s.cellRange(bound[0], bound[1], bound[2], bound[3], bound[4], bound[5])
	for ix := cells[0]; ix < cells[1]; ix++ {
		for iy := cells[2]; iy < cells[3]; iy++ {
			for iz := cells[4]; iz < cells[5]; iz++ {
				key := cellKey{ix, iy, iz}
				bucket := s.cells[key]
				// A full slice expression forces a fresh array, so a fork still holding the old
				// bucket never sees this index.
				s.cells[key] = append(bucket[:len(bucket):len(bucket)], index)
			}
		}
	}
}

// Query is the bound indices sharing at least one cell with the box, each once, in first-seen
// order: cells are walked x, then y, then z, as the reference walks them. The slice may be a
// bucket of the index itself, so the caller must not modify it.
func (s *SpatialIndex) Query(x1, y1, z1, x2, y2, z2 int64) []int {
	return s.collect(s.cellRange(x1, y1, z1, x2, y2, z2))
}

// bucket is the bounds filed under the cell containing a point.
func (s *SpatialIndex) bucket(x, y, z int64) []int {
	return s.cells[cellKey{x / s.cellX, y / s.cellY, z / s.cellZ}]
}

// collect walks the cells of one range in x, then y, then z order. A range touching a single
// non-empty bucket returns that bucket as it is, allocating nothing; merging starts only when
// a second bucket appears.
func (s *SpatialIndex) collect(cells cellRange) []int {
	var first, found []int
	merging := false
	for ix := cells[0]; ix < cells[1]; ix++ {
		for iy := cells[2]; iy < cells[3]; iy++ {
			for iz := cells[4]; iz < cells[5]; iz++ {
				bucket := s.cells[cellKey{ix, iy, iz}]
				switch {
				case len(bucket) == 0:
					continue
				case first == nil:
					first = bucket
					continue
				case !merging:
					found, merging = s.startMerge(first, len(bucket)), true
				}
				for _, index := range bucket {
					if s.mark(index) {
						found = append(found, index)
					}
				}
			}
		}
	}
	if merging {
		return found
	}
	return first
}

// startMerge begins a fresh deduplicated answer holding first, which has no duplicates of its
// own.
func (s *SpatialIndex) startMerge(first []int, more int) []int {
	s.nextStamp()
	found := make([]int, 0, len(first)+more)
	for _, index := range first {
		s.mark(index)
		found = append(found, index)
	}
	return found
}

func (s *SpatialIndex) nextStamp() {
	s.stamp++
	if s.stamp == 0 {
		for index := range s.seen {
			s.seen[index] = 0
		}
		s.stamp = 1
	}
}

// mark reports whether index is new to the current query.
func (s *SpatialIndex) mark(index int) bool {
	if index >= len(s.seen) {
		grown := make([]uint32, 2*index+16)
		copy(grown, s.seen)
		s.seen = grown
	}
	if s.seen[index] == s.stamp {
		return false
	}
	s.seen[index] = s.stamp
	return true
}

func (s *SpatialIndex) cellRange(x1, y1, z1, x2, y2, z2 int64) cellRange {
	return cellRange{x1 / s.cellX, ceilDiv(ints.Max(x2, x1+1), s.cellX),
		y1 / s.cellY, ceilDiv(ints.Max(y2, y1+1), s.cellY),
		z1 / s.cellZ, ceilDiv(ints.Max(z2, z1+1), s.cellZ)}
}

func ceilDiv(numerator, denominator int64) int64 { return (numerator + denominator - 1) / denominator }
