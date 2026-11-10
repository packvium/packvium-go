package constraint

import (
	"sort"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/internal/wide"
)

// Convex hull and tipping geometry.
//
// Overlap area is not stability: an item can be supported over enough area and still
// overhang its own centre of gravity. Everything here is exact integer arithmetic, with no
// trigonometry or division; cross products are 128-bit, so doubled coordinates never
// overflow.

// Point2D is a point in the footprint plane, in (usually doubled) ticks.
type Point2D struct{ X, Y int64 }

func cross(o, a, b Point2D) int {
	left := wide.Mul(a.X-o.X, b.Y-o.Y)
	return left.Sub(wide.Mul(a.Y-o.Y, b.X-o.X)).Sign()
}

// ConvexHull is Andrew's monotone chain, O(p log p): the hull counter-clockwise with
// duplicates collapsed. Degenerate input (0, 1 or all-collinear points) returns that same
// degenerate shape: a single contact point or a razor-thin strip is a real, if precarious,
// physical case, not an error.
func ConvexHull(points []Point2D) []Point2D {
	sorted := append([]Point2D(nil), points...)
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].X != sorted[b].X {
			return sorted[a].X < sorted[b].X
		}
		return sorted[a].Y < sorted[b].Y
	})
	unique := sorted[:0]
	for _, point := range sorted {
		if len(unique) == 0 || unique[len(unique)-1] != point {
			unique = append(unique, point)
		}
	}
	if len(unique) <= 2 {
		return unique
	}
	lower := chain(unique)
	reversed := make([]Point2D, len(unique))
	for index, point := range unique {
		reversed[len(unique)-1-index] = point
	}
	upper := chain(reversed)
	hull := append(lower[:len(lower)-1:len(lower)-1], upper[:len(upper)-1]...)
	if len(hull) == 0 {
		return unique
	}
	return hull
}

func chain(points []Point2D) []Point2D {
	var half []Point2D
	for _, point := range points {
		for len(half) >= 2 && cross(half[len(half)-2], half[len(half)-1], point) <= 0 {
			half = half[:len(half)-1]
		}
		half = append(half, point)
	}
	return half
}

func onSegment(p, a, b Point2D) bool {
	if cross(a, b, p) != 0 {
		return false
	}
	return ints.Min(a.X, b.X) <= p.X && p.X <= ints.Max(a.X, b.X) && ints.Min(a.Y, b.Y) <= p.Y && p.Y <= ints.Max(a.Y, b.Y)
}

// PointInHull reports whether point lies inside or on the boundary of a hull from
// ConvexHull. O(h).
func PointInHull(point Point2D, hull []Point2D) bool {
	switch len(hull) {
	case 0:
		return false
	case 1:
		return point == hull[0]
	case 2:
		return onSegment(point, hull[0], hull[1])
	}
	for index := range hull {
		if cross(hull[index], hull[(index+1)%len(hull)], point) < 0 {
			return false
		}
	}
	return true
}

// ContactHullPoints is the doubled-tick corners of every candidate/supporter overlap
// rectangle. Doubled so a centroid computed as x1 + x2 is an exact integer even when a
// box's length or width is odd.
func ContactHullPoints(candidate domain.Box, supporters []domain.Box) []Point2D {
	cx1, cy1, cx2, cy2 := candidate.Origin().X(), candidate.Origin().Y(), candidate.X2(), candidate.Y2()
	var points []Point2D
	for _, box := range supporters {
		ox1, oy1 := ints.Max(cx1, box.Origin().X()), ints.Max(cy1, box.Origin().Y())
		ox2, oy2 := ints.Min(cx2, box.X2()), ints.Min(cy2, box.Y2())
		if ox2 <= ox1 || oy2 <= oy1 {
			continue
		}
		for _, x := range [2]int64{ox1, ox2} {
			for _, y := range [2]int64{oy1, oy2} {
				points = append(points, Point2D{ints.MulExact(2, x), ints.MulExact(2, y)})
			}
		}
	}
	return points
}

// DoubledCentroid is twice the box's footprint centroid, exact for odd extents too.
func DoubledCentroid(box domain.Box) Point2D {
	return Point2D{box.Origin().X() + box.X2(), box.Origin().Y() + box.Y2()}
}
