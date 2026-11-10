// Package result holds the immutable outcome of one packing call.
package result

import (
	"errors"

	"github.com/toxakara/packvium-go/domain"
)

// PackingStatus is the overall verdict of a result.
type PackingStatus string

const (
	Optimal       PackingStatus = "optimal"
	Feasible      PackingStatus = "feasible"
	BestFound     PackingStatus = "best_found"
	TimeLimit     PackingStatus = "time_limit"
	Infeasible    PackingStatus = "infeasible"
	InvalidResult PackingStatus = "invalid_result"
)

// ResultFact is one open, forward-compatible result axis. The code is a string rather than an
// enum so an older client can preserve a value a newer producer introduces. Attributes keep
// their insertion order through AttributeKeys.
type ResultFact struct {
	code       string
	keys       []string
	attributes map[string]interface{}
}

// NewResultFact is a fact with no attributes.
func NewResultFact(code string) ResultFact { return ResultFact{code: code} }

// NewResultFactWith is a fact whose attributes are given as alternating key, value pairs.
func NewResultFactWith(code string, keysAndValues ...interface{}) ResultFact {
	fact := ResultFact{code: code, attributes: map[string]interface{}{}}
	for index := 0; index+1 < len(keysAndValues); index += 2 {
		key := keysAndValues[index].(string)
		fact.keys = append(fact.keys, key)
		fact.attributes[key] = keysAndValues[index+1]
	}
	return fact
}

func (f ResultFact) Code() string { return f.code }

// Attribute returns one attribute value.
func (f ResultFact) Attribute(key string) (interface{}, bool) {
	value, ok := f.attributes[key]
	return value, ok
}

// WithCode is the same fact under another code.
func (f ResultFact) WithCode(code string) ResultFact {
	f.code = code
	return f
}

// ToMap is the wire form: the code plus every attribute.
func (f ResultFact) ToMap() map[string]interface{} {
	rendered := map[string]interface{}{"code": f.code}
	for _, key := range f.keys {
		rendered[key] = f.attributes[key]
	}
	return rendered
}

// SolverMetrics are the search counters of the winning start.
type SolverMetrics struct {
	CandidatePointsConsidered int64
	OrientationsConsidered    int64
	FeasibleCandidates        int64
	CollisionChecks           int64
	SupportChecks             int64
	SpacePartitions           int64
	SearchNodesExpanded       int64
}

// ToMap is the wire form.
func (m SolverMetrics) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"candidate_points_considered": m.CandidatePointsConsidered, "orientations_considered": m.OrientationsConsidered,
		"feasible_candidates": m.FeasibleCandidates, "collision_checks": m.CollisionChecks,
		"support_checks": m.SupportChecks, "space_partitions": m.SpacePartitions,
		"search_nodes_expanded": m.SearchNodesExpanded,
	}
}

// AlgorithmReport describes how the answer was searched for; it is a diagnostic, not part of
// the language-independent contract.
type AlgorithmReport struct {
	Profile             string
	Solver              string
	DurationMS          int64
	Seed                int64
	TimeLimitReached    bool
	EffortLimitReached  bool
	CandidatesEvaluated int64
	PlacementsAttempted int64
	Metrics             SolverMetrics
}

// StartRecord is how one portfolio start ended, and whether its answer is the one returned.
type StartRecord struct {
	ID                    string
	Started               bool
	Completed             bool
	Truncated             bool
	Selected              bool
	GlobalDeadlineReached bool
}

// ToMap is the wire form.
func (s StartRecord) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id": s.ID, "started": s.Started, "completed": s.Completed, "truncated": s.Truncated,
		"selected": s.Selected, "global_deadline_reached": s.GlobalDeadlineReached,
	}
}

// AggregateTermination folds every start's ending into one termination fact. O(starts).
func AggregateTermination(starts []StartRecord, failed bool) (ResultFact, error) {
	if len(starts) == 0 {
		return ResultFact{}, errors.New("termination aggregation requires at least one start record")
	}
	var selected []StartRecord
	anyTruncated, allCompleted, globalDeadline := false, true, false
	rendered := make([]interface{}, len(starts))
	for index, start := range starts {
		if start.Selected {
			selected = append(selected, start)
		}
		anyTruncated = anyTruncated || start.Truncated
		allCompleted = allCompleted && start.Completed
		globalDeadline = globalDeadline || start.GlobalDeadlineReached
		rendered[index] = start.ToMap()
	}
	if len(selected) != 1 {
		return ResultFact{}, errors.New("termination aggregation requires exactly one selected start")
	}
	winningTruncated := selected[0].Truncated
	code := "complete"
	switch {
	case failed:
		code = "error"
	case winningTruncated || globalDeadline:
		code = "time_limit"
	}
	return NewResultFactWith(code,
		"any_start_truncated", anyTruncated, "all_required_starts_completed", allCompleted,
		"winning_start_truncated", winningTruncated, "global_deadline_reached", globalDeadline,
		"starts", rendered), nil
}

// PackingResult is one ranked answer: its containers, what it left behind, and the facts
// about how it was found.
type PackingResult struct {
	Status       PackingStatus
	Containers   []*domain.PackedContainer
	Unpacked     []domain.UnpackedItem
	Algorithm    AlgorithmReport
	Score        []int64
	Warnings     []string
	Alternatives []*PackingResult
	Feasibility  ResultFact
	Termination  ResultFact
	Optimality   ResultFact
	Objective    string
}

// NewPackingResult derives feasibility and optimality from the status.
func NewPackingResult(status PackingStatus, containers []*domain.PackedContainer, unpacked []domain.UnpackedItem,
	algorithm AlgorithmReport, score []int64, warnings []string, termination ResultFact, objective string) *PackingResult {
	complete := len(unpacked) == 0
	return &PackingResult{
		Status: status, Containers: containers, Unpacked: unpacked, Algorithm: algorithm, Score: score,
		Warnings: warnings, Feasibility: NewResultFact(feasibility(status, complete)), Termination: termination,
		Optimality: NewResultFact(optimality(status, complete)), Objective: objective,
	}
}

// WithAlternatives is a shallow copy carrying the chosen alternatives.
func (r *PackingResult) WithAlternatives(chosen []*PackingResult) *PackingResult {
	copied := *r
	copied.Alternatives = chosen
	return &copied
}

// Complete reports whether every requested item was placed.
func (r *PackingResult) Complete() bool { return len(r.Unpacked) == 0 }

// PackedItemCount counts every placement, lattice instances included.
func (r *PackingResult) PackedItemCount() int64 {
	total := int64(0)
	for _, container := range r.Containers {
		total += container.PlacementCount()
	}
	return total
}

func feasibility(status PackingStatus, complete bool) string {
	switch {
	case status == Infeasible:
		return "infeasible"
	case complete && status != InvalidResult:
		return "feasible"
	}
	return "unknown"
}

func optimality(status PackingStatus, complete bool) string {
	switch {
	case status == Optimal:
		return "proven_optimal"
	case status == Infeasible:
		return "proven_infeasible"
	case !complete && status != InvalidResult:
		return "best_found"
	}
	return "not_proven"
}
