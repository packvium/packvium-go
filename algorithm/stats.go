package algorithm

import "github.com/packvium/packvium-go/result"

// SearchStats are per-start mutable work counters; they live only for one solve.
type SearchStats struct {
	CandidatesEvaluated       int64
	PlacementsAttempted       int64
	CandidatePointsConsidered int64
	CollisionChecks           int64
	SupportChecks             int64
	SpacePartitions           int64
	SearchNodesExpanded       int64
	// objectiveLowerBound is the request-level lower bound on the objective vector, computed
	// at the root of an exact_small or plan-beam solve. Deliberately absent from Metrics: those
	// are serialised into every result, and a gap is not yet a public result field.
	objectiveLowerBound []int64
}

// Metrics is the public form of the counters.
func (s *SearchStats) Metrics() result.SolverMetrics {
	return result.SolverMetrics{
		CandidatePointsConsidered: s.CandidatePointsConsidered, OrientationsConsidered: s.PlacementsAttempted,
		FeasibleCandidates: s.CandidatesEvaluated, CollisionChecks: s.CollisionChecks,
		SupportChecks: s.SupportChecks, SpacePartitions: s.SpacePartitions,
		SearchNodesExpanded: s.SearchNodesExpanded,
	}
}

// ObjectiveLowerBound is a copy of the recorded bound, or nil when none was recorded.
func (s *SearchStats) ObjectiveLowerBound() []int64 {
	if s.objectiveLowerBound == nil {
		return nil
	}
	return append([]int64(nil), s.objectiveLowerBound...)
}

// SetObjectiveLowerBound records a copy of bound.
func (s *SearchStats) SetObjectiveLowerBound(bound []int64) {
	s.objectiveLowerBound = append([]int64{}, bound...)
}
