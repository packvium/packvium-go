package algorithm

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/hull"
	"github.com/toxakara/packvium-go/internal/ints"
	"github.com/toxakara/packvium-go/units"
)

// ContainerState is placed boxes plus the candidate points they expose; mutable, owned by one
// search branch.
//
// Points are corner points of every solid together with their projections onto the surfaces
// below, behind and to the left of them. A point is discarded only when it falls outside the
// container or inside a solid, never for being "dominated": a lower-left point can be blocked
// while the point it would dominate is usable.
type ContainerState struct {
	Container  *domain.Container
	Sequence   int64
	Placements []domain.Placement
	// points are the free points in the order they were found; the axle scan reads them in
	// this order. pointSet answers membership.
	points   []domain.Point
	pointSet map[domain.Point]struct{}
	// OrderedPoints are the same points in deterministic (z, y, x) order, which the candidate
	// scan walks.
	OrderedPoints []domain.Point
	// Bounds are the plain extents of everything solid, obstacles first.
	Bounds []Bound
	// HullShapes is parallel to Bounds: the solid's rotated hull, or nil for an ordinary box.
	HullShapes           []*hull.Shape
	PayloadTicks         int64
	UsedVolume           *big.Int // exact physical volume, nesting overlap removed
	StackSensitive       bool
	RouteSensitive       bool
	CompressionSensitive bool
	MaxZ                 int64
	Index                *SpatialIndex
	// LatticeSummary is set instead of filling Placements when the grid's lattice fast path
	// applies.
	LatticeSummary *domain.LatticeSummary
	LatticeItems   []domain.ItemInstance
	usableVolume   *big.Int
}

// NewContainerState is an empty container: its obstacles are solid and the origin plus every
// point an obstacle exposes are free.
func NewContainerState(container *domain.Container, sequence int64) *ContainerState {
	inner := container.InnerDimensions()
	state := &ContainerState{
		Container: container, Sequence: sequence, pointSet: map[domain.Point]struct{}{}, UsedVolume: new(big.Int),
		Index: NewSpatialIndex(inner.Length().Ticks(), inner.Width().Ticks(), inner.Height().Ticks()),
	}
	var obstacles []domain.Box
	for _, obstacle := range container.Obstacles() {
		for _, box := range obstacle.Boxes() {
			obstacles = append(obstacles, box)
			state.addSolid(box, nil)
		}
	}
	state.absorb([]domain.Point{{}})
	for _, box := range obstacles {
		state.absorb(state.exposedPoints(box))
	}
	return state
}

// Copy is a fork for a search branch; the constructor's point seeding is not repeated.
func (s *ContainerState) Copy() *ContainerState {
	child := *s
	child.Placements = append([]domain.Placement(nil), s.Placements...)
	child.points = append([]domain.Point(nil), s.points...)
	child.pointSet = make(map[domain.Point]struct{}, len(s.pointSet))
	for point := range s.pointSet {
		child.pointSet[point] = struct{}{}
	}
	child.OrderedPoints = append([]domain.Point(nil), s.OrderedPoints...)
	// Bounds and hull shapes are never mutated after insertion, so the elements are shared.
	child.Bounds = append([]Bound(nil), s.Bounds...)
	child.HullShapes = append([]*hull.Shape(nil), s.HullShapes...)
	child.Index = s.Index.Copy()
	return &child
}

// Points are the free points in the order they were found.
func (s *ContainerState) Points() []domain.Point { return s.points }

// UsableVolume is the void-fill-reserve headroom; it depends only on the container, so it is
// computed once.
func (s *ContainerState) UsableVolume() *big.Int {
	if s.usableVolume == nil {
		s.usableVolume = constraint.UsableVolume(s.Container)
	}
	return s.usableVolume
}

func boundOf(box domain.Box) Bound {
	origin := box.Origin()
	return Bound{origin.X(), origin.Y(), origin.Z(), box.X2(), box.Y2(), box.Z2()}
}

func (s *ContainerState) addSolid(box domain.Box, shape *hull.Shape) {
	bound := boundOf(box)
	s.Index.Add(len(s.Bounds), bound)
	s.Bounds = append(s.Bounds, bound)
	s.HullShapes = append(s.HullShapes, shape)
}

// Add places a solid and updates the free points around it.
func (s *ContainerState) Add(placement domain.Placement) {
	box := placement.EnvelopeBox()
	nowCompressionSensitive := s.CompressionSensitive || placement.Instance().Item().ShapeType() == domain.Compressible
	if nowCompressionSensitive {
		withNew := append(append([]domain.Placement(nil), s.Placements...), placement)
		s.UsedVolume = domain.UsedVolume(AssignTopLoads(withNew))
	} else {
		s.UsedVolume = new(big.Int).Add(s.UsedVolume, domain.UsedVolumeDelta(s.Placements, placement))
	}
	s.recordPlacement(placement, box)
	s.CompressionSensitive = nowCompressionSensitive
	shape := placement.HullShape()
	s.addSolid(box, shape)
	// Retiring a point because it lies inside a solid's box assumes the box is the solid. For a
	// hull it is not: a wedge leaves most of its box, origin included, to the next item, and
	// pruning those points would hide an interlocking pack.
	if shape == nil {
		s.retirePointsInside(box)
	}
	s.absorb(s.exposedPoints(box))
}

func (s *ContainerState) retirePointsInside(box domain.Box) {
	retired := false
	kept := s.points[:0:0]
	for _, point := range s.points {
		if box.ContainsPoint(point) {
			delete(s.pointSet, point)
			retired = true
			continue
		}
		kept = append(kept, point)
	}
	if !retired {
		return
	}
	s.points = kept
	ordered := make([]domain.Point, 0, len(s.OrderedPoints))
	for _, point := range s.OrderedPoints {
		if _, free := s.pointSet[point]; free {
			ordered = append(ordered, point)
		}
	}
	s.OrderedPoints = ordered
}

// AddDirect records a placement for solvers that never query extreme points. The grid builds
// a non-overlapping lattice directly, so maintaining the point index would be quadratic waste.
func (s *ContainerState) AddDirect(placement domain.Placement) {
	s.UsedVolume = new(big.Int).Add(s.UsedVolume, domain.UsedVolumeDelta(s.Placements, placement))
	s.recordPlacement(placement, placement.EnvelopeBox())
}

// PlacementCount counts lattice instances too.
func (s *ContainerState) PlacementCount() int64 {
	if s.LatticeSummary != nil {
		return s.LatticeSummary.Count
	}
	return int64(len(s.Placements))
}

// AddLattice records a whole regular-lattice run in O(items) without building a placement
// each.
func (s *ContainerState) AddLattice(summary *domain.LatticeSummary, items []domain.ItemInstance) {
	s.LatticeSummary = summary
	s.LatticeItems = append([]domain.ItemInstance(nil), items...)
	s.PayloadTicks = ints.AddExact(s.PayloadTicks, summary.TotalWeightTicks())
	s.UsedVolume = new(big.Int).Add(s.UsedVolume, summary.UsedVolume())
	for _, instance := range items {
		s.StackSensitive = s.StackSensitive || instance.Item().IsStackSensitive()
		s.RouteSensitive = s.RouteSensitive || instance.Item().StopIndex() != nil
	}
	s.MaxZ = ints.Max(s.MaxZ, summary.MaxZTicks())
}

func (s *ContainerState) recordPlacement(placement domain.Placement, box domain.Box) {
	item := placement.Instance().Item()
	s.Placements = append(s.Placements, placement)
	s.PayloadTicks = ints.AddExact(s.PayloadTicks, item.Weight().Ticks())
	s.StackSensitive = s.StackSensitive || item.IsStackSensitive()
	s.RouteSensitive = s.RouteSensitive || item.StopIndex() != nil
	s.MaxZ = ints.Max(s.MaxZ, box.Z2())
}

// PlacementSignature is id@x,y,z|... over the placements in order: the last-resort tie-break
// every solver's node key ends with. O(m).
func (s *ContainerState) PlacementSignature() string {
	var signature strings.Builder
	for index, placement := range s.Placements {
		if index > 0 {
			signature.WriteByte('|')
		}
		origin := placement.EnvelopeOrigin()
		signature.WriteString(placement.Instance().ID())
		signature.WriteByte('@')
		signature.WriteString(strconv.FormatInt(origin.X(), 10))
		signature.WriteByte(',')
		signature.WriteString(strconv.FormatInt(origin.Y(), 10))
		signature.WriteByte(',')
		signature.WriteString(strconv.FormatInt(origin.Z(), 10))
	}
	return signature.String()
}

// absorb adds the candidates that are inside the container, new and not inside a solid.
// O(points x solids) for the inside test, plus O(points) per sorted insertion.
func (s *ContainerState) absorb(candidates []domain.Point) {
	inner := s.Container.InnerDimensions()
	length, width, height := inner.Length().Ticks(), inner.Width().Ticks(), inner.Height().Ticks()
	for _, point := range candidates {
		if point.X() >= length || point.Y() >= width || point.Z() >= height {
			continue
		}
		if _, known := s.pointSet[point]; known || s.insideSolid(point) {
			continue
		}
		s.pointSet[point] = struct{}{}
		s.points = append(s.points, point)
		at := s.insertionIndex(point)
		s.OrderedPoints = append(s.OrderedPoints, domain.Point{})
		copy(s.OrderedPoints[at+1:], s.OrderedPoints[at:])
		s.OrderedPoints[at] = point
	}
}

func (s *ContainerState) insideSolid(point domain.Point) bool {
	x, y, z := point.X(), point.Y(), point.Z()
	for _, b := range s.Bounds {
		if b[0] <= x && x < b[3] && b[1] <= y && y < b[4] && b[2] <= z && z < b[5] {
			return true
		}
	}
	return false
}

// insertionIndex is the upper bound in (z, y, x) order, so equal keys keep insertion order.
func (s *ContainerState) insertionIndex(point domain.Point) int {
	low, high := 0, len(s.OrderedPoints)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if compareZYX(s.OrderedPoints[middle], point) <= 0 {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low
}

func compareZYX(left, right domain.Point) int {
	if order := compareInts(left.Z(), right.Z()); order != 0 {
		return order
	}
	if order := compareInts(left.Y(), right.Y()); order != 0 {
		return order
	}
	return compareInts(left.X(), right.X())
}

func (s *ContainerState) exposedPoints(box domain.Box) []domain.Point {
	o := box.Origin()
	x1, y1, z1, x2, y2, z2 := o.X(), o.Y(), o.Z(), box.X2(), box.Y2(), box.Z2()
	return []domain.Point{
		domain.MustPoint(x2, y1, z1), domain.MustPoint(x1, y2, z1), domain.MustPoint(x1, y1, z2),
		domain.MustPoint(x2, y2, z1), domain.MustPoint(x2, y1, z2), domain.MustPoint(x1, y2, z2),
		domain.MustPoint(x2, y1, s.surfaceZ(x2, y1, z1)),
		domain.MustPoint(x2, s.surfaceY(x2, z1, y1), z1),
		domain.MustPoint(x1, y2, s.surfaceZ(x1, y2, z1)),
		domain.MustPoint(s.surfaceX(y2, z1, x1), y2, z1),
		domain.MustPoint(x1, s.surfaceY(x1, z2, y1), z2),
		domain.MustPoint(s.surfaceX(y1, z2, x1), y1, z2),
	}
}

func (s *ContainerState) surfaceZ(x, y, ceiling int64) int64 {
	best := int64(0)
	for _, b := range s.Bounds {
		if b[5] <= ceiling && b[0] <= x && x < b[3] && b[1] <= y && y < b[4] && b[5] > best {
			best = b[5]
		}
	}
	return best
}

func (s *ContainerState) surfaceY(x, z, ceiling int64) int64 {
	best := int64(0)
	for _, b := range s.Bounds {
		if b[4] <= ceiling && b[0] <= x && x < b[3] && b[2] <= z && z < b[5] && b[4] > best {
			best = b[4]
		}
	}
	return best
}

func (s *ContainerState) surfaceX(y, z, ceiling int64) int64 {
	best := int64(0)
	for _, b := range s.Bounds {
		if b[3] <= ceiling && b[1] <= y && y < b[4] && b[2] <= z && z < b[5] && b[3] > best {
			best = b[3]
		}
	}
	return best
}

// AssignTopLoads fills in each placement's borne load once the container is final.
func AssignTopLoads(placements []domain.Placement) []domain.Placement {
	loads := constraint.TopLoads(constraint.Units(placements))
	out := make([]domain.Placement, len(placements))
	for index, placement := range placements {
		out[index] = placement.WithTopLoad(units.MustWeight(loads[index]))
	}
	return out
}
