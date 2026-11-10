package hull

import (
	"fmt"

	"github.com/toxakara/packvium-go/internal/ints"
	"github.com/toxakara/packvium-go/internal/wide"
)

// Shape is a convex hull's separating axes in its own local frame, computed once per item
// and rotation and shared by every candidate position: a placement contributes only an
// offset.
type Shape struct {
	vertices       []Vertex
	faceAxes       []Axis
	edgeDirections []Axis
	volume         wide.Int128
}

// Vertices returns a copy of the shape's vertices.
func (s *Shape) Vertices() []Vertex { return append([]Vertex(nil), s.vertices...) }

// Volume is the exact occupied volume in cubic ticks.
func (s *Shape) Volume() wide.Int128 { return s.volume }

// NewShape builds a hull's axes, faces and volume from its vertices.
func NewShape(vertices []Vertex) (*Shape, error) {
	points, err := Validate(vertices)
	if err != nil {
		return nil, err
	}
	normals := map[Axis]struct{}{}
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			for k := j + 1; k < len(points); k++ {
				first := points[i]
				axis, ok := primitive(cross(subtract(points[j], first), subtract(points[k], first)))
				if ok && isSupporting(points, first, axis) {
					normals[axis] = struct{}{}
				}
			}
		}
	}
	faceAxes := sortedAxes(normals)
	faces := woundFaces(points, faceAxes)
	return &Shape{vertices: points, faceAxes: faceAxes, edgeDirections: edgeDirections(faces), volume: volumeOf(faces)}, nil
}

// BoxShape is a cuboid, built in O(1) without searching for its own faces: its normals
// and edges are exactly the three unit axes.
func BoxShape(length, width, height int64) *Shape {
	vertices := make([]Vertex, 0, 8)
	for _, x := range []int64{0, 1} {
		for _, y := range []int64{0, 1} {
			for _, z := range []int64{0, 1} {
				vertices = append(vertices, Vertex{x * length, y * width, z * height})
			}
		}
	}
	return &Shape{
		vertices: vertices, faceAxes: unitAxes, edgeDirections: unitAxes,
		volume: wide.Mul(length, width).Mul64(height),
	}
}

// projection is the closed interval of the shape on axis, in local coordinates.
func (s *Shape) projection(axis Axis) (wide.Int128, wide.Int128) {
	low := dot(s.vertices[0], Vertex(axis))
	high := low
	for _, vertex := range s.vertices[1:] {
		value := dot(vertex, Vertex(axis))
		if value.Cmp(low) < 0 {
			low = value
		}
		if value.Cmp(high) > 0 {
			high = value
		}
	}
	return low, high
}

// Validate canonicalises an authored vertex list or refuses a hull with no interior.
func Validate(vertices []Vertex) ([]Vertex, error) {
	points := append([]Vertex(nil), vertices...)
	if len(points) < MinimumVertices {
		return nil, degenerate("a convex hull needs at least %d vertices, got %d", MinimumVertices, len(points))
	}
	seen := make(map[Vertex]struct{}, len(points))
	for _, point := range points {
		seen[point] = struct{}{}
	}
	if len(seen) != len(points) {
		return nil, degenerate("convex hull vertices must be unique")
	}
	for _, point := range points {
		for _, component := range point {
			if absolute(component) > MaxCoordinate {
				return nil, degenerate("convex hull coordinates must stay within %d ticks", MaxCoordinate)
			}
		}
	}
	if encloses(points) {
		return points, nil
	}
	return nil, degenerate("convex hull vertices are coplanar and enclose no volume")
}

// encloses reports whether any four points span a tetrahedron of non-zero volume. O(v^4)
// in the worst case, which is the coplanar one; a solid hull exits at its first
// non-degenerate quadruple.
func encloses(points []Vertex) bool {
	for a := 0; a < len(points); a++ {
		for b := a + 1; b < len(points); b++ {
			for c := b + 1; c < len(points); c++ {
				normal := cross(subtract(points[b], points[a]), subtract(points[c], points[a]))
				for d := c + 1; d < len(points); d++ {
					if dot(subtract(points[d], points[a]), normal).Sign() != 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

// SeparatingAxes is both hulls' face normals plus every edge-against-edge direction,
// deduplicated and sorted.
func SeparatingAxes(left, right *Shape) []Axis {
	axes := map[Axis]struct{}{}
	for _, axis := range left.faceAxes {
		axes[axis] = struct{}{}
	}
	for _, axis := range right.faceAxes {
		axes[axis] = struct{}{}
	}
	for _, leftEdge := range left.edgeDirections {
		for _, rightEdge := range right.edgeDirections {
			if axis, ok := primitive(cross(Vertex(leftEdge), Vertex(rightEdge))); ok {
				axes[axis] = struct{}{}
			}
		}
	}
	return sortedAxes(axes)
}

// Collide reports whether two placed hulls overlap with positive volume. Touching is
// contact, not collision, matching the half-open convention of axis-aligned boxes.
func Collide(left *Shape, leftOrigin Vertex, right *Shape, rightOrigin Vertex) bool {
	for _, axis := range SeparatingAxes(left, right) {
		leftLow, leftHigh := left.projection(axis)
		rightLow, rightHigh := right.projection(axis)
		leftShift := dot(leftOrigin, Vertex(axis))
		rightShift := dot(rightOrigin, Vertex(axis))
		if leftHigh.Add(leftShift).Cmp(rightLow.Add(rightShift)) <= 0 ||
			rightHigh.Add(rightShift).Cmp(leftLow.Add(leftShift)) <= 0 {
			return false
		}
	}
	return true
}

// sourceAxes says which local axis each container axis takes its extent from, per rotation.
var sourceAxes = map[string][3]int{
	"LWH": {0, 1, 2}, "LHW": {0, 2, 1},
	"WLH": {1, 0, 2}, "WHL": {1, 2, 0},
	"HLW": {2, 0, 1}, "HWL": {2, 1, 0},
}

func permutationIsOdd(axes [3]int) bool {
	inversions := 0
	for first := 0; first < 3; first++ {
		for second := first + 1; second < 3; second++ {
			if axes[first] > axes[second] {
				inversions++
			}
		}
	}
	return inversions%2 == 1
}

// Rotate reorients a hull the way a box's dimensions are rotated. An odd permutation flips
// one axis so every rotation is proper and never a mirror image. The result is translated
// so its bounding box starts at the origin.
func Rotate(vertices []Vertex, rotation string) ([]Vertex, error) {
	axes, ok := sourceAxes[rotation]
	if !ok {
		return nil, fmt.Errorf("unknown rotation %q", rotation)
	}
	sign := int64(1)
	if permutationIsOdd(axes) {
		sign = -1
	}
	turned := make([]Vertex, len(vertices))
	for index, vertex := range vertices {
		turned[index] = Vertex{sign * vertex[axes[0]], vertex[axes[1]], vertex[axes[2]]}
	}
	lower, _ := BoundingExtent(turned)
	for index := range turned {
		turned[index] = subtract(turned[index], lower)
	}
	return turned, nil
}

// ShapeFor is the rotated hull of one item in one orientation.
func ShapeFor(vertices []Vertex, rotation string) (*Shape, error) {
	rotated, err := Rotate(vertices, rotation)
	if err != nil {
		return nil, err
	}
	return NewShape(rotated)
}

// BoundingExtent returns the inclusive lower and upper corners of the hull's envelope.
func BoundingExtent(vertices []Vertex) (Vertex, Vertex) {
	lower, upper := vertices[0], vertices[0]
	for _, vertex := range vertices[1:] {
		for axis := range vertex {
			lower[axis] = ints.Min(lower[axis], vertex[axis])
			upper[axis] = ints.Max(upper[axis], vertex[axis])
		}
	}
	return lower, upper
}
