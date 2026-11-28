package validation

import (
	"strings"
)

// The result's own claims about the search: counters, counted-work limits, start records and
// the three result facts.

var solverMetrics = []string{"candidate_points_considered", "orientations_considered", "feasible_candidates",
	"collision_checks", "support_checks", "space_partitions", "search_nodes_expanded"}

func algorithmOf(result interface{}) interface{} {
	algorithm := member(result, "algorithm")
	if !isDict(algorithm) {
		panic(contractRefusal("algorithm must be an object"))
	}
	return algorithm
}

func checkAlgorithmMetrics(result interface{}, issues *[]ResultIssue) {
	algorithm := algorithmOf(result)
	metrics := get(algorithm, "metrics", nil)
	wellFormed := isDict(metrics)
	for _, field := range solverMetrics {
		if !wellFormed {
			break
		}
		count := strictInt(get(metrics, field, nil))
		wellFormed = count != nil && count.Sign() >= 0
	}
	if !wellFormed {
		*issues = append(*issues, ResultIssue{"solver_metrics_mismatch",
			"algorithm.metrics must contain every non-negative integer counter"})
		return
	}
	candidates := strictInt(get(algorithm, "candidates_evaluated", nil))
	attempts := strictInt(get(algorithm, "placements_attempted", nil))
	if candidates == nil || attempts == nil || candidates.Cmp(strictInt(get(metrics, "feasible_candidates", nil))) != 0 ||
		attempts.Cmp(strictInt(get(metrics, "orientations_considered", nil))) != 0 {
		*issues = append(*issues, ResultIssue{"solver_metrics_mismatch", "legacy candidate counters must mirror algorithm.metrics"})
	}
}

// checkEffortBudget: counted-work limits are respected, and the stop flag is evidenced by
// one.
func checkEffortBudget(request, result interface{}, issues *[]ResultIssue) {
	algorithm := member(result, "algorithm")
	effortLimited, isFlag := get(algorithm, "effort_limit_reached", nil).(bool)
	if !isFlag {
		panic(contractRefusal("algorithm.effort_limit_reached must be a boolean"))
	}
	budget := get(get(request, "configuration", newJSONObject()), "effort_budget", nil)
	if budget == nil {
		if effortLimited {
			*issues = append(*issues, ResultIssue{"effort_budget_mismatch",
				"effort_limit_reached is true but the request has no effort_budget"})
		}
		return
	}
	if !isDict(budget) {
		panic(contractRefusal("config.effort_budget must be an object"))
	}
	metrics := get(algorithm, "metrics", nil)
	if !isDict(metrics) {
		return
	}
	reached := false
	for _, pair := range [][2]string{{"max_candidates_evaluated", "feasible_candidates"},
		{"max_placement_attempts", "orientations_considered"}, {"max_search_nodes", "search_nodes_expanded"}} {
		limit := get(budget, pair[0], nil)
		value := strictInt(get(metrics, pair[1], nil))
		if limit == nil || value == nil {
			continue
		}
		order := compare(value, limit)
		if order > 0 {
			*issues = append(*issues, ResultIssue{"effort_budget_exceeded",
				pair[1] + " is " + value.String() + ", above " + pair[0] + "=" + pyStr(limit)})
		}
		reached = reached || order >= 0
	}
	restartLimit := get(budget, "max_restarts", nil)
	starts := get(get(result, "termination", newJSONObject()), "starts", []interface{}{})
	if limit := intInstance(restartLimit); limit != nil && isList(starts) {
		started := int64(0)
		for _, start := range starts.([]interface{}) {
			if isDict(start) && get(start, "started", nil) == true {
				started++
			}
		}
		if bi(started).Cmp(limit) > 0 {
			*issues = append(*issues, ResultIssue{"effort_budget_exceeded",
				"started " + bi(started).String() + " solver runs, above max_restarts=" + pyStr(restartLimit)})
		}
		reached = reached || bi(started).Cmp(limit) >= 0
	}
	if effortLimited && !reached {
		*issues = append(*issues, ResultIssue{"effort_budget_mismatch",
			"effort_limit_reached is true but no configured counted-work boundary was reached"})
	}
}

func flagOf(algorithm interface{}, name string) bool {
	value, ok := get(algorithm, name, nil).(bool)
	if !ok {
		panic(contractRefusal("algorithm." + name + " must be a boolean"))
	}
	return value
}

func factCode(result interface{}, name string) string {
	fact := member(result, name)
	if !isDict(fact) {
		panic(contractRefusal(name + " must be an object"))
	}
	code, ok := get(fact, "code", nil).(string)
	if !ok || code == "" {
		panic(contractRefusal(name + ".code must be a non-empty string"))
	}
	return code
}

func checkResultFacts(result interface{}, issues *[]ResultIssue) {
	algorithm := algorithmOf(result)
	timedOut := flagOf(algorithm, "time_limit_reached")
	effortLimited := flagOf(algorithm, "effort_limit_reached")
	if timedOut && effortLimited {
		*issues = append(*issues, ResultIssue{"result_fact_mismatch", "time_limit_reached and effort_limit_reached cannot both be true"})
	}
	termination := member(result, "termination")
	if !isDict(termination) {
		panic(contractRefusal("termination must be an object"))
	}
	checkStarts(termination, algorithm, timedOut, effortLimited, issues)
	status := member(result, "status")
	complete := truthy(member(result, "complete"))
	invalid := isText(status, "invalid_result")
	feasibility, termination_, optimality := "unknown", "complete", "not_proven"
	switch {
	case isText(status, "infeasible"):
		feasibility = "infeasible"
	case complete && !invalid:
		feasibility = "feasible"
	}
	switch {
	case invalid:
		termination_ = "error"
	case timedOut:
		termination_ = "time_limit"
	case effortLimited:
		termination_ = "effort_limit"
	}
	switch {
	case isText(status, "optimal"):
		optimality = "proven_optimal"
	case isText(status, "infeasible"):
		optimality = "proven_infeasible"
	case !complete && !invalid:
		optimality = "best_found"
	}
	for _, fact := range [][2]string{{"feasibility", feasibility}, {"termination", termination_}, {"optimality", optimality}} {
		if actual := factCode(result, fact[0]); actual != fact[1] {
			*issues = append(*issues, ResultIssue{"result_fact_mismatch",
				fact[0] + ".code is " + pyRepr(actual) + ", expected " + pyRepr(fact[1])})
		}
	}
}

func isTrueAt(start interface{}, name string) bool {
	return isDict(start) && get(start, name, nil) == true
}

func checkStarts(termination, algorithm interface{}, timedOut, effortLimited bool, issues *[]ResultIssue) {
	starts, ok := get(termination, "starts", nil).([]interface{})
	if !ok || len(starts) == 0 {
		*issues = append(*issues, ResultIssue{"termination_aggregate_mismatch", "termination.starts must contain at least one start"})
		return
	}
	malformed := false
	for _, start := range starts {
		if !isDict(start) {
			malformed = true
			continue
		}
		if _, named := get(start, "id", nil).(string); !named {
			malformed = true
			continue
		}
		var flags []bool
		for _, name := range []string{"started", "completed", "truncated", "selected", "global_deadline_reached"} {
			if flag, isFlag := get(start, name, nil).(bool); isFlag {
				flags = append(flags, flag)
			}
		}
		if len(flags) != 5 {
			malformed = true
			continue
		}
		started, completed, truncated := flags[0], flags[1], flags[2]
		if (completed && (!started || truncated)) || (truncated && (!started || completed)) ||
			(!started && (completed || truncated)) {
			*issues = append(*issues, ResultIssue{"start_record_mismatch", member(start, "id").(string) + ": inconsistent lifecycle flags"})
		}
	}
	if malformed {
		*issues = append(*issues, ResultIssue{"start_record_mismatch", "a start record is missing its id or boolean lifecycle fields"})
	}
	var selected []interface{}
	for _, start := range starts {
		if isTrueAt(start, "selected") {
			selected = append(selected, start)
		}
	}
	if len(selected) != 1 {
		*issues = append(*issues, ResultIssue{"termination_aggregate_mismatch",
			"expected one selected start, found " + bi(int64(len(selected))).String()})
		return
	}
	for _, start := range starts {
		if !isDict(start) {
			return
		}
	}
	winner := selected[0]
	anyStart := func(name string) bool {
		for _, start := range starts {
			if isTrueAt(start, name) {
				return true
			}
		}
		return false
	}
	allStarts := func(name string) bool {
		for _, start := range starts {
			if !isTrueAt(start, name) {
				return false
			}
		}
		return true
	}
	deadline, winnerTruncated := anyStart("global_deadline_reached"), isTrueAt(winner, "truncated")
	for _, aggregate := range []struct {
		name     string
		expected bool
	}{{"any_start_truncated", anyStart("truncated")}, {"all_required_starts_completed", allStarts("completed")},
		{"winning_start_truncated", winnerTruncated}, {"global_deadline_reached", deadline}} {
		if reported := get(termination, aggregate.name, nil); reported != aggregate.expected {
			*issues = append(*issues, ResultIssue{"termination_aggregate_mismatch",
				aggregate.name + " is " + pyRepr(reported) + ", expected " + boolRepr(aggregate.expected)})
		}
	}
	solver, named := get(algorithm, "solver", nil).(string)
	selectedID := get(winner, "id", "")
	if named && !isText(selectedID, solver) && !strings.HasPrefix(text(selectedID, "a start id"), solver+"#") {
		*issues = append(*issues, ResultIssue{"winning_start_mismatch",
			"selected start " + pyRepr(selectedID) + " does not identify solver " + pyRepr(solver)})
	}
	if deadline && !timedOut {
		*issues = append(*issues, ResultIssue{"legacy_time_limit_mismatch", "global_deadline_reached requires time_limit_reached"})
	}
	if (deadline || winnerTruncated) != (timedOut || effortLimited) {
		*issues = append(*issues, ResultIssue{"termination_limit_mismatch", "selected-start truncation must identify exactly one reached limit"})
	}
}
