package hull

import "github.com/toxakara/packvium-go/internal/wide"

// orderedFace gift-wraps the corners of one planar convex face into cyclic order seen from
// outside. Vertices lying inside the face or along an edge are skipped: collinear
// candidates resolve to the farthest one.
func orderedFace(face []Vertex, outward Axis) []Vertex {
	start := face[0]
	for _, vertex := range face[1:] {
		if lessVertex(vertex, start) {
			start = vertex
		}
	}
	ordered := []Vertex{start}
	current := start
	for range face {
		following, found := Vertex{}, false
		for _, candidate := range face {
			if candidate == current {
				continue
			}
			if !found {
				following, found = candidate, true
				continue
			}
			turn := dot(cross(subtract(following, current), subtract(candidate, current)), Vertex(outward)).Sign()
			if turn < 0 || (turn == 0 && squareLength(subtract(candidate, current)) > squareLength(subtract(following, current))) {
				following = candidate
			}
		}
		if !found || following == start {
			break
		}
		ordered = append(ordered, following)
		current = following
	}
	return ordered
}

// woundFaces returns every face of the hull, each as its corners in outward cyclic order.
// Each canonical axis stands for up to two opposite faces; a supporting plane carrying
// fewer than three vertices is an edge or a corner, not a face.
func woundFaces(vertices []Vertex, faceAxes []Axis) [][]Vertex {
	var faces [][]Vertex
	for _, axis := range faceAxes {
		for _, outward := range []Axis{axis, {-axis[0], -axis[1], -axis[2]}} {
			extreme := dot(vertices[0], Vertex(outward))
			for _, vertex := range vertices[1:] {
				if value := dot(vertex, Vertex(outward)); value.Cmp(extreme) > 0 {
					extreme = value
				}
			}
			var face []Vertex
			for _, vertex := range vertices {
				if dot(vertex, Vertex(outward)).Cmp(extreme) == 0 {
					face = append(face, vertex)
				}
			}
			if len(face) < 3 {
				continue
			}
			faces = append(faces, orderedFace(face, outward))
		}
	}
	return faces
}

// volumeOf is the exact volume by the divergence theorem: six times the volume is the sum,
// over outward-oriented surface triangles, of a . (b x c).
func volumeOf(faces [][]Vertex) wide.Int128 {
	var sixVolumes wide.Int128
	for _, ordered := range faces {
		apex := ordered[0]
		for index := 1; index+1 < len(ordered); index++ {
			sixVolumes = sixVolumes.Add(dot(apex, cross(ordered[index], ordered[index+1])))
		}
	}
	if sixVolumes.Sign() < 0 {
		sixVolumes = sixVolumes.Neg()
	}
	return sixVolumes.QuoSmall(6)
}

// edgeDirections are the hull's real edges -- consecutive corners of each wound face --
// deduplicated, canonical and sorted.
func edgeDirections(faces [][]Vertex) []Axis {
	edges := map[Axis]struct{}{}
	for _, ordered := range faces {
		for index, start := range ordered {
			end := ordered[(index+1)%len(ordered)]
			edges[reduce(subtract(end, start))] = struct{}{}
		}
	}
	return sortedAxes(edges)
}
