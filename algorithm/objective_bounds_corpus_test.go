package algorithm

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

type sceneCase struct {
	Fixture   string `json:"fixture"`
	Bounds    []int64
	Instances []struct {
		Shrinks bool   `json:"shrinks"`
		Volume  string `json:"volume"`
		Weight  int64  `json:"weight"`
	} `json:"instances"`
	Containers []struct {
		BaseArea  int64  `json:"base_area"`
		CostMinor int64  `json:"cost_minor"`
		Height    int64  `json:"height"`
		Inner     string `json:"inner"`
		MaxItems  *int64 `json:"max_items"`
		Payload   *int64 `json:"payload"`
		Quantity  *int64 `json:"quantity"`
		Usable    string `json:"usable"`
	} `json:"containers"`
}

func bigOf(t *testing.T, text string) *big.Int {
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		t.Fatalf("not an integer: %s", text)
	}
	return value
}

// sceneRef is the milestone this tree implements; PACKVIUM_CONFORMANCE_REF replays another
// tag, as it does for the conformance corpus.
func sceneRef() string {
	if ref := os.Getenv("PACKVIUM_CONFORMANCE_REF"); ref != "" {
		return ref
	}
	return "v1.1.0"
}

// TestMatchesTheSharedBoundCorpus recomputes the vectors the reference wrote at the tag. It
// skips when the corpus cannot be read from git.
func TestMatchesTheSharedBoundCorpus(t *testing.T) {
	content, err := exec.Command("git", "show", sceneRef()+":conformance/scene/objective-bounds.json").Output()
	if err != nil {
		t.Skipf("objective-bounds scene unavailable: %v", err)
	}
	var document struct {
		Cases []sceneCase `json:"cases"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	for _, scene := range document.Cases {
		instances := make([]NormalisedInstance, len(scene.Instances))
		for index, instance := range scene.Instances {
			instances[index] = NormalisedInstance{bigOf(t, instance.Volume), instance.Weight, instance.Shrinks}
		}
		containers := make([]NormalisedContainer, len(scene.Containers))
		for index, container := range scene.Containers {
			containers[index] = NormalisedContainer{bigOf(t, container.Usable), bigOf(t, container.Inner),
				container.BaseArea, container.Height, container.Payload, container.MaxItems, container.Quantity,
				container.CostMinor}
		}
		if got := BoundsFromNormalised(instances, containers); !reflect.DeepEqual(got, scene.Bounds) {
			t.Errorf("%s: %v, want %v", scene.Fixture, got, scene.Bounds)
		}
	}
	if len(document.Cases) == 0 {
		t.Fatal("no cases")
	}
}
