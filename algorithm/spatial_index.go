package algorithm

import "github.com/toxakara/packvium-go/internal/ints"

const cellsPerAxis int64 = 8

type cellKey struct{ x, y, z int64 }

// Bound is a solid's plain integer extent [x1, y1, z1, x2, y2, z2]; the candidate scan tests
// these millions of times.
type Bound [6]int64

// SpatialIndex is a uniform-grid broad phase for axis-aligned box collision queries. A cell
// assignment only needs to be a superset of the boxes a query could intersect: Query returns
// indices and the caller still runs the exact test on each, so an error here can only make the
// index slower, never admit a collision. A query costs O(cells touched + boxes in them)
// instead of O(all boxes).
type SpatialIndex struct {
	cellX, cellY, cellZ int64
	cells               map[cellKey][]int
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

// Copy is a structural fork, independent of the original from here on. O(entries).
func (s *SpatialIndex) Copy() *SpatialIndex {
	cells := make(map[cellKey][]int, len(s.cells))
	for key, indices := range s.cells {
		cells[key] = append([]int(nil), indices...)
	}
	return &SpatialIndex{cellX: s.cellX, cellY: s.cellY, cellZ: s.cellZ, cells: cells}
}

// Add files a bound under every cell it touches.
func (s *SpatialIndex) Add(index int, bound Bound) {
	x1, x2, y1, y2, z1, z2 := s.cellRange(bound[0], bound[1], bound[2], bound[3], bound[4], bound[5])
	for ix := x1; ix < x2; ix++ {
		for iy := y1; iy < y2; iy++ {
			for iz := z1; iz < z2; iz++ {
				key := cellKey{ix, iy, iz}
				s.cells[key] = append(s.cells[key], index)
			}
		}
	}
}

// Query is the bound indices sharing at least one cell with the box, each once, in first-seen
// order: cells are walked x, then y, then z, as the reference walks them.
func (s *SpatialIndex) Query(x1, y1, z1, x2, y2, z2 int64) []int {
	cx1, cx2, cy1, cy2, cz1, cz2 := s.cellRange(x1, y1, z1, x2, y2, z2)
	s.stamp++
	if s.stamp == 0 {
		for index := range s.seen {
			s.seen[index] = 0
		}
		s.stamp = 1
	}
	var found []int
	for ix := cx1; ix < cx2; ix++ {
		for iy := cy1; iy < cy2; iy++ {
			for iz := cz1; iz < cz2; iz++ {
				for _, index := range s.cells[cellKey{ix, iy, iz}] {
					if s.mark(index) {
						found = append(found, index)
					}
				}
			}
		}
	}
	return found
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

func (s *SpatialIndex) cellRange(x1, y1, z1, x2, y2, z2 int64) (int64, int64, int64, int64, int64, int64) {
	return x1 / s.cellX, ceilDiv(ints.Max(x2, x1+1), s.cellX),
		y1 / s.cellY, ceilDiv(ints.Max(y2, y1+1), s.cellY),
		z1 / s.cellZ, ceilDiv(ints.Max(z2, z1+1), s.cellZ)
}

func ceilDiv(numerator, denominator int64) int64 { return (numerator + denominator - 1) / denominator }
