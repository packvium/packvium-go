// Package hull is exact separating-axis collision for convex_hull items.
//
// Every coordinate is bounded by MaxCoordinate, so differences stay within 2*10^8 and
// cross products of differences within 8*10^16 -- inside int64. Dot products of a vertex
// or origin with such an axis can reach 10^36 and are carried in a 128-bit integer.
//
// Complexity: building a Shape is O(v^4) (O(v^3) candidate faces, each checked against
// O(v) vertices) and is paid once per item and rotation. Collide is
// O((f_a + f_b + e_a*e_b) * (a + b)) for f face axes and e edge directions.
package hull

import (
	"fmt"
	"sort"

	"github.com/packvium/packvium-go/internal/wide"
)

// Vertex is a point in an item's local tick frame. Unlike a placement point it may be
// negative: a hull is authored around whatever origin its author chose.
type Vertex [3]int64

// Axis is a canonical direction: gcd divided out, first non-zero component positive.
type Axis [3]int64

const (
	// MinimumVertices is the smallest number of vertices that can enclose a volume.
	MinimumVertices = 4
	// MaxCoordinate is the largest absolute vertex coordinate in ticks (6.25 m), shared
	// verbatim with every other engine.
	MaxCoordinate = 100_000_000
)

// unitAxes are both the face normals and the edge directions of any axis-aligned box.
var unitAxes = []Axis{{0, 0, 1}, {0, 1, 0}, {1, 0, 0}}

// DegenerateError refuses hull vertices that do not enclose a three-dimensional volume.
// Rejected rather than repaired: a flat vertex set has no interior, so every collision
// answer about it would be a vacuous "no collision".
type DegenerateError struct{ message string }

func (e *DegenerateError) Error() string { return e.message }

func degenerate(format string, arguments ...interface{}) error {
	return &DegenerateError{message: fmt.Sprintf(format, arguments...)}
}

func subtract(left, right Vertex) Vertex {
	return Vertex{left[0] - right[0], left[1] - right[1], left[2] - right[2]}
}

func cross(left, right Vertex) Vertex {
	return Vertex{
		left[1]*right[2] - left[2]*right[1],
		left[2]*right[0] - left[0]*right[2],
		left[0]*right[1] - left[1]*right[0],
	}
}

func dot(point Vertex, axis Vertex) wide.Int128 {
	return wide.Mul(point[0], axis[0]).Add(wide.Mul(point[1], axis[1])).Add(wide.Mul(point[2], axis[2]))
}

func squareLength(vector Vertex) int64 {
	return vector[0]*vector[0] + vector[1]*vector[1] + vector[2]*vector[2]
}

func gcd(left, right int64) int64 {
	for right != 0 {
		left, right = right, left%right
	}
	return left
}

func absolute(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// reduce divides out the gcd and fixes the sign so parallel axes collapse to one entry.
// The caller guarantees a non-zero vector.
func reduce(vector Vertex) Axis {
	divisor := gcd(gcd(absolute(vector[0]), absolute(vector[1])), absolute(vector[2]))
	reduced := Axis{vector[0] / divisor, vector[1] / divisor, vector[2] / divisor}
	for _, component := range reduced {
		if component > 0 {
			return reduced
		}
		if component < 0 {
			return Axis{-reduced[0], -reduced[1], -reduced[2]}
		}
	}
	return reduced
}

// primitive is reduce, or false for the zero vector, which names no direction.
func primitive(vector Vertex) (Axis, bool) {
	if vector == (Vertex{}) {
		return Axis{}, false
	}
	return reduce(vector), true
}

func lessVertex(left, right [3]int64) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return false
}

func sortedAxes(set map[Axis]struct{}) []Axis {
	axes := make([]Axis, 0, len(set))
	for axis := range set {
		axes = append(axes, axis)
	}
	sort.Slice(axes, func(i, j int) bool { return lessVertex(axes[i], axes[j]) })
	return axes
}

// isSupporting reports whether the plane through origin with normal axis leaves every
// vertex on one side.
func isSupporting(vertices []Vertex, origin Vertex, axis Axis) bool {
	seenAbove, seenBelow := false, false
	offset := dot(origin, Vertex(axis))
	for _, vertex := range vertices {
		switch dot(vertex, Vertex(axis)).Cmp(offset) {
		case 1:
			seenAbove = true
		case -1:
			seenBelow = true
		}
		if seenAbove && seenBelow {
			return false
		}
	}
	return true
}
