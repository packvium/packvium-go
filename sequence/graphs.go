package sequence

import (
	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
)

// DependencyGraph is, per placement index, the indices it depends on. Unloading and loading
// graphs share the shape and the cycle check; they differ in what a dependency means.
type DependencyGraph struct{ dependsOn [][]int }

// NewDependencyGraph is a graph over indices 0..len(dependsOn)-1, so a test can build a
// synthetic, non-geometric one to prove the cycle check. Every dependency must be in range.
func NewDependencyGraph(dependsOn [][]int) DependencyGraph {
	copied := make([][]int, len(dependsOn))
	for index, dependencies := range dependsOn {
		copied[index] = sortedCopy(dependencies)
	}
	return DependencyGraph{copied}
}

// Len is the number of nodes.
func (g DependencyGraph) Len() int { return len(g.dependsOn) }

// DependsOn is what index depends on, ascending.
func (g DependencyGraph) DependsOn(index int) []int { return append([]int(nil), g.dependsOn[index]...) }

// IsAcyclic is a depth-first cycle check, O(V + E).
func (g DependencyGraph) IsAcyclic() bool {
	visiting := make([]bool, len(g.dependsOn))
	visited := make([]bool, len(g.dependsOn))
	var visit func(node int) bool
	visit = func(node int) bool {
		if visited[node] {
			return true
		}
		if visiting[node] {
			return false
		}
		visiting[node] = true
		for _, dependency := range g.dependsOn[node] {
			if !visit(dependency) {
				return false
			}
		}
		visiting[node], visited[node] = false, true
		return true
	}
	for node := range g.dependsOn {
		if !visit(node) {
			return false
		}
	}
	return true
}

// BuildUnloadingGraph is the unloading dependency graph: each index depends on its
// children, whatever rests on it, which must come off first. Children sit strictly above,
// so following edges strictly increases height. O(n + E) after the contact graph.
func BuildUnloadingGraph(boxes []domain.Box) DependencyGraph {
	contacts := constraint.NewContactGraph(boxes, 1)
	dependsOn := make([][]int, len(boxes))
	for index := range boxes {
		dependsOn[index] = contacts.Children(index)
	}
	return NewDependencyGraph(dependsOn)
}

// BuildLoadingGraph is the loading dependency graph: each index depends on its supporters,
// whatever it rests on, which must already be present. Supporters sit strictly below, so
// following edges strictly decreases height.
func BuildLoadingGraph(boxes []domain.Box) DependencyGraph {
	contacts := constraint.NewContactGraph(boxes, 1)
	dependsOn := make([][]int, len(boxes))
	for index := range boxes {
		for _, edge := range contacts.Supporters(index) {
			dependsOn[index] = append(dependsOn[index], edge.Index)
		}
	}
	return NewDependencyGraph(dependsOn)
}
