package hull

import (
	"encoding/json"
	"io/ioutil"
	"reflect"
	"testing"
)

// testdata/reference-cases.json was produced by the reference engine's hull module at
// v1.0.0: rotations, canonical axes, exact volumes, collision verdicts and refusals.
type referenceCases struct {
	Shapes []struct {
		Vertices       []Vertex `json:"vertices"`
		Rotation       string   `json:"rotation"`
		Rotated        []Vertex `json:"rotated"`
		FaceAxes       []Axis   `json:"face_axes"`
		EdgeDirections []Axis   `json:"edge_directions"`
		Volume         string   `json:"volume"`
	} `json:"shapes"`
	Collisions []struct {
		Left        []Vertex `json:"left"`
		LeftOrigin  Vertex   `json:"left_origin"`
		Right       []Vertex `json:"right"`
		RightOrigin Vertex   `json:"right_origin"`
		Collide     bool     `json:"collide"`
	} `json:"collisions"`
	Errors []struct {
		Vertices []Vertex `json:"vertices"`
		Error    *string  `json:"error"`
	} `json:"errors"`
}

func loadReferenceCases(t *testing.T) referenceCases {
	t.Helper()
	raw, err := ioutil.ReadFile("testdata/reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases referenceCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func mustShape(t *testing.T, vertices []Vertex) *Shape {
	t.Helper()
	shape, err := NewShape(vertices)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestShapesMatchReference(t *testing.T) {
	for _, c := range loadReferenceCases(t).Shapes {
		rotated, err := Rotate(c.Vertices, c.Rotation)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(rotated, c.Rotated) {
			t.Errorf("rotate %v %s: got %v, want %v", c.Vertices, c.Rotation, rotated, c.Rotated)
			continue
		}
		shape := mustShape(t, rotated)
		if !reflect.DeepEqual(shape.faceAxes, c.FaceAxes) {
			t.Errorf("%v: face axes %v, want %v", rotated, shape.faceAxes, c.FaceAxes)
		}
		if !reflect.DeepEqual(shape.edgeDirections, c.EdgeDirections) {
			t.Errorf("%v: edge directions %v, want %v", rotated, shape.edgeDirections, c.EdgeDirections)
		}
		if got := shape.Volume().Big().String(); got != c.Volume {
			t.Errorf("%v: volume %s, want %s", rotated, got, c.Volume)
		}
	}
}

func TestCollisionsMatchReference(t *testing.T) {
	for _, c := range loadReferenceCases(t).Collisions {
		got := Collide(mustShape(t, c.Left), c.LeftOrigin, mustShape(t, c.Right), c.RightOrigin)
		if got != c.Collide {
			t.Errorf("collide %v@%v with %v@%v = %t, want %t", c.Left, c.LeftOrigin, c.Right, c.RightOrigin, got, c.Collide)
		}
	}
}

func TestRefusalsMatchReference(t *testing.T) {
	for _, c := range loadReferenceCases(t).Errors {
		_, err := Validate(c.Vertices)
		switch {
		case c.Error == nil && err != nil:
			t.Errorf("%v: refused with %q", c.Vertices, err)
		case c.Error != nil && (err == nil || err.Error() != *c.Error):
			t.Errorf("%v: got %v, want %q", c.Vertices, err, *c.Error)
		}
	}
}

func TestBoxShapeCollidesLikeItsHull(t *testing.T) {
	box := BoxShape(4, 4, 4)
	cube := mustShape(t, box.Vertices())
	for _, origin := range []Vertex{{0, 0, 0}, {3, 3, 3}, {4, 0, 0}, {0, 4, 0}, {2, 5, 1}} {
		if Collide(box, Vertex{}, box, origin) != Collide(cube, Vertex{}, cube, origin) {
			t.Errorf("box and searched cube disagree at %v", origin)
		}
	}
}
