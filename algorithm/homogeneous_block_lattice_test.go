package algorithm

import (
	"testing"

	"github.com/packvium/packvium-go/config"
)

// perNode is the enumeration the block solver made one node at a time before the group was
// summarised at once: every node counted, the first largest count in (nx, ny) order kept.
func perNode(key latticeKey) latticeSummary {
	var summary latticeSummary
	for nx := int64(1); nx <= key.columns; nx++ {
		maximumNY := key.rows
		if limit := key.capacity / nx; limit < maximumNY {
			maximumNY = limit
		}
		for ny := int64(1); ny <= maximumNY; ny++ {
			summary.nodes++
			nz := key.layers
			if limit := key.capacity / (nx * ny); limit < nz {
				nz = limit
			}
			if nz > 0 && nx*ny*nz > summary.count {
				summary.count, summary.nx, summary.ny, summary.nz = nx*ny*nz, nx, ny, nz
			}
		}
	}
	return summary
}

func TestAGroupSummaryIsThePerNodeEnumeration(t *testing.T) {
	for columns := int64(1); columns <= 7; columns++ {
		for rows := int64(1); rows <= 7; rows++ {
			for layers := int64(0); layers <= 4; layers++ {
				for capacity := columns; capacity <= 60; capacity += 7 {
					key := latticeKey{columns, rows, layers, capacity}
					if got, want := blockLattice(key), perNode(key); got != want {
						t.Fatalf("%v: %v, want %v", key, got, want)
					}
				}
			}
		}
	}
}

func TestOnlyAnAttachedNodeBudgetCapsAGroup(t *testing.T) {
	stats := &SearchStats{}
	limit := int64(5)
	if ceiling := DeadlineOfMilliseconds(60_000).searchNodeCeiling(stats); ceiling != nil {
		t.Fatal("a deadline with no budget has a node ceiling")
	}
	budgeted := DeadlineOfMilliseconds(60_000).WithEffort(effortOfNodes(limit), stats)
	if ceiling := budgeted.searchNodeCeiling(stats); ceiling == nil || *ceiling != limit {
		t.Fatalf("ceiling %v, want %d", ceiling, limit)
	}
	if budgeted.searchNodeCeiling(&SearchStats{}) != nil {
		t.Fatal("a budget that reads another stats has a node ceiling")
	}
}

func effortOfNodes(limit int64) *config.EffortBudget {
	return &config.EffortBudget{MaxSearchNodes: &limit}
}
