// Package pareto is a Pareto report over benchmark or scenario results, one per profile.
//
// A single blended score hides the trade-off: a report that turns utilisation, runtime and
// container count into one weighted number can name a "winner" that is worse on the one axis
// a caller actually cares about. This package never computes such a number. A result
// dominates another only if it is no worse on every named axis and strictly better on at
// least one; the report per profile is the set of results nothing else dominates, the Pareto
// frontier, not a ranking.
//
// When exactly one result survives as non-dominated it is the named winner. When more than
// one survives -- a genuine trade-off, one engine faster and another denser -- the report
// says so (no winner, every survivor listed) rather than breaking the tie with an arbitrary
// or blended pick.
//
// The package is pure report generation over caller-supplied metrics. It does not run
// anything, and it assumes every candidate it receives already passed the caller's
// validation: a disqualified candidate must be excluded before it gets here.
package pareto

import (
	"fmt"
	"math"
	"sort"

	"github.com/packvium/packvium-go/internal/pyrepr"
)

// ParetoReportError is the base of every error this package returns that is not a plain
// argument error. errors.As matches the more specific errors below as one too.
type ParetoReportError struct{ message string }

// Error is the message, in the reference's wording.
func (e *ParetoReportError) Error() string { return e.message }

// InconsistentAxesError is two candidates that do not report every compared axis. An axis
// missing from one side would silently drop out of the dominance comparison.
type InconsistentAxesError struct{ message string }

// Error is the message, in the reference's wording.
func (e *InconsistentAxesError) Error() string { return e.message }

// Unwrap makes the error match *ParetoReportError, its base in the reference.
func (e *InconsistentAxesError) Unwrap() error { return &ParetoReportError{e.message} }

// NonFiniteMetricError is a metric that is NaN, which cannot take part in a dominance
// comparison.
//
// Dominance is decided by > and <, and both are false for NaN, so an axis carrying one sets
// neither better nor worse and is silently counted as equal: a candidate whose metrics were
// all NaN would land on the frontier because nothing could dominate it. Infinities are
// allowed on purpose: they are ends of the number line, not holes in it, so a caller encoding
// "unpriceable" as an infinite cost gets exactly the dominance answer they mean.
type NonFiniteMetricError struct{ message string }

// Error is the message, in the reference's wording.
func (e *NonFiniteMetricError) Error() string { return e.message }

// Unwrap makes the error match *ParetoReportError, its base in the reference.
func (e *NonFiniteMetricError) Unwrap() error { return &ParetoReportError{e.message} }

// refuseNaN guards one candidate's metrics in O(axes). where names the candidate, because an
// evaluator producing NaN is a bug upstream and a message that does not point at it only
// moves the search. Axes are visited in sorted order, so the axis named is the same on every
// run.
func refuseNaN(metrics map[string]float64, where string) error {
	for _, axis := range sortedAxes(metrics) {
		if math.IsNaN(metrics[axis]) {
			return &NonFiniteMetricError{fmt.Sprintf("%s: metric %s is NaN and cannot be compared", where, pyrepr.String(axis))}
		}
	}
	return nil
}

func sortedAxes(metrics map[string]float64) []string {
	axes := make([]string, 0, len(metrics))
	for axis := range metrics {
		axes = append(axes, axis)
	}
	sort.Strings(axes)
	return axes
}

func copyMetrics(metrics map[string]float64) map[string]float64 {
	copied := make(map[string]float64, len(metrics))
	for axis, value := range metrics {
		copied[axis] = value
	}
	return copied
}

// CandidateResult is one engine's result on one profile, already validated by the caller.
type CandidateResult struct {
	profile, engine string
	metrics         map[string]float64
}

// NewCandidateResult refuses a candidate without a profile, an engine or metrics, and one
// carrying a NaN. Checked at construction, so an invalid candidate cannot exist and the
// report need not check it once per pair.
func NewCandidateResult(profile, engine string, metrics map[string]float64) (CandidateResult, error) {
	switch {
	case profile == "":
		return CandidateResult{}, fmt.Errorf("profile is required")
	case engine == "":
		return CandidateResult{}, fmt.Errorf("engine is required")
	case len(metrics) == 0:
		return CandidateResult{}, fmt.Errorf("metrics cannot be empty")
	}
	if err := refuseNaN(metrics, fmt.Sprintf("%s on profile %s", engine, profile)); err != nil {
		return CandidateResult{}, err
	}
	return CandidateResult{profile, engine, copyMetrics(metrics)}, nil
}

// Profile is the profile the candidate ran on.
func (c CandidateResult) Profile() string { return c.profile }

// Engine is the name the report lists the candidate under.
func (c CandidateResult) Engine() string { return c.engine }

// Metrics is a copy of the candidate's metric axes.
func (c CandidateResult) Metrics() map[string]float64 { return copyMetrics(c.metrics) }

// ProfileReport is one profile's Pareto frontier. Winner is set only when exactly one
// candidate survived as non-dominated; a genuine multi-way trade-off leaves it empty and
// lists every surviving engine in ParetoOptimal instead.
type ProfileReport struct {
	profile                  string
	paretoOptimal, dominated []string
	winner                   string
}

// NewProfileReport refuses a report whose winner is not its only Pareto-optimal engine. An
// empty winner is none, which an engine name never is.
func NewProfileReport(profile string, paretoOptimal, dominated []string, winner string) (ProfileReport, error) {
	if profile == "" {
		return ProfileReport{}, fmt.Errorf("profile is required")
	}
	if winner != "" && !contains(paretoOptimal, winner) {
		return ProfileReport{}, fmt.Errorf("winner, when set, must be one of the Pareto-optimal engines")
	}
	if winner != "" && len(paretoOptimal) != 1 {
		return ProfileReport{}, fmt.Errorf("winner can only be set when exactly one candidate is Pareto-optimal")
	}
	return ProfileReport{profile, append([]string(nil), paretoOptimal...), append([]string(nil), dominated...), winner}, nil
}

// Profile is the profile the report is about.
func (r ProfileReport) Profile() string { return r.profile }

// ParetoOptimal is every engine nothing dominates, sorted.
func (r ProfileReport) ParetoOptimal() []string { return append([]string(nil), r.paretoOptimal...) }

// Dominated is every other engine, sorted.
func (r ProfileReport) Dominated() []string { return append([]string(nil), r.dominated...) }

// Winner is the only Pareto-optimal engine, or empty when there is a trade-off.
func (r ProfileReport) Winner() string { return r.winner }

func contains(words []string, wanted string) bool {
	for _, word := range words {
		if word == wanted {
			return true
		}
	}
	return false
}

// Dominates is whether a dominates b: no worse on every axis in higherIsBetter and strictly
// better on at least one. An axis either side lacks is an *InconsistentAxesError and a NaN on
// either side a *NonFiniteMetricError, because a silent partial comparison is worse than an
// explicit error. Axes outside higherIsBetter are not compared. O(axes).
func Dominates(a, b map[string]float64, higherIsBetter map[string]bool) (bool, error) {
	var missing []string
	for axis := range higherIsBetter {
		_, inA := a[axis]
		_, inB := b[axis]
		if !inA || !inB {
			missing = append(missing, axis)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return false, &InconsistentAxesError{"missing metric axis/axes: " + listRepr(missing)}
	}
	// Public and taking bare maps, so it cannot rely on CandidateResult having checked them.
	if err := refuseNaN(a, "left candidate"); err != nil {
		return false, err
	}
	if err := refuseNaN(b, "right candidate"); err != nil {
		return false, err
	}
	atLeastAsGood, strictlyBetterSomewhere := true, false
	for axis, higherWins := range higherIsBetter {
		left, right := a[axis], b[axis]
		better, worse := left < right, left > right
		if higherWins {
			better, worse = left > right, left < right
		}
		if worse {
			atLeastAsGood = false
		}
		if better {
			strictlyBetterSomewhere = true
		}
	}
	return atLeastAsGood && strictlyBetterSomewhere, nil
}

// listRepr is the reference's repr of a list of strings.
func listRepr(words []string) string {
	text := "["
	for index, word := range words {
		if index > 0 {
			text += ", "
		}
		text += pyrepr.String(word)
	}
	return text + "]"
}

// frontier splits one profile's candidates into the non-dominated and the dominated engines,
// each sorted. With two axes it is a sort and one sweep, O(c log c) for c candidates, the
// size of one profile; otherwise O(c^2 · axes). Candidates the comparison would refuse (a
// missing axis, a NaN metric) take the pairwise path, so the error is the one Dominates
// reports, in the same order.
func frontier(candidates []CandidateResult, higherIsBetter map[string]bool) ([]string, []string, error) {
	if len(higherIsBetter) != 2 || len(candidates) < 2 || !comparable(candidates, higherIsBetter) {
		return pairwiseFrontier(candidates, higherIsBetter)
	}
	optimal := twoAxisFrontier(orientedVectors(candidates, higherIsBetter))
	var onFrontier, dominated []string
	for index, candidate := range candidates {
		if optimal[index] {
			onFrontier = append(onFrontier, candidate.engine)
		} else {
			dominated = append(dominated, candidate.engine)
		}
	}
	sort.Strings(onFrontier)
	sort.Strings(dominated)
	return onFrontier, dominated, nil
}

// comparable is whether every candidate has every axis and no NaN metric, so Dominates
// would refuse none of the pairs.
func comparable(candidates []CandidateResult, higherIsBetter map[string]bool) bool {
	for _, candidate := range candidates {
		for axis := range higherIsBetter {
			if _, present := candidate.metrics[axis]; !present {
				return false
			}
		}
		for _, value := range candidate.metrics {
			if math.IsNaN(value) {
				return false
			}
		}
	}
	return true
}

// orientedVectors are the candidates' metrics on the axes in name order, negated where
// higher is better, so smaller is better on every axis.
func orientedVectors(candidates []CandidateResult, higherIsBetter map[string]bool) [][2]float64 {
	axes := make([]string, 0, len(higherIsBetter))
	for axis := range higherIsBetter {
		axes = append(axes, axis)
	}
	sort.Strings(axes)
	vectors := make([][2]float64, len(candidates))
	for index, candidate := range candidates {
		for position, axis := range axes {
			value := candidate.metrics[axis]
			if higherIsBetter[axis] {
				value = -value
			}
			vectors[index][position] = value
		}
	}
	return vectors
}

// twoAxisFrontier is the set of vectors no other vector is at least as small as on both axes
// and smaller on one. Sorted by (x, y), a vector survives when its y is below every y seen at
// a smaller x; within one x only the smallest y survives, and equal vectors survive together.
func twoAxisFrontier(vectors [][2]float64) map[int]bool {
	ordered := make([]int, len(vectors))
	for index := range ordered {
		ordered[index] = index
	}
	sort.SliceStable(ordered, func(a, b int) bool {
		left, right := vectors[ordered[a]], vectors[ordered[b]]
		return left[0] < right[0] || (left[0] == right[0] && left[1] < right[1])
	})
	optimal := map[int]bool{}
	bestY, seen := 0.0, false
	for position := 0; position < len(ordered); {
		x, y := vectors[ordered[position]][0], vectors[ordered[position]][1]
		survives := !seen || y < bestY
		end := position
		for ; end < len(ordered) && vectors[ordered[end]][0] == x; end++ {
			if survives && vectors[ordered[end]][1] == y {
				optimal[ordered[end]] = true
			}
		}
		if survives {
			bestY, seen = y, true
		}
		position = end
	}
	return optimal
}

// pairwiseFrontier compares every candidate with every other through Dominates.
// O(c^2 · axes).
func pairwiseFrontier(candidates []CandidateResult, higherIsBetter map[string]bool) ([]string, []string, error) {
	var optimal, dominated []string
	for index, candidate := range candidates {
		isDominated := false
		for otherIndex, other := range candidates {
			if otherIndex == index {
				continue
			}
			beats, err := Dominates(other.metrics, candidate.metrics, higherIsBetter)
			if err != nil {
				return nil, nil, err
			}
			if beats {
				isDominated = true
				break
			}
		}
		if isDominated {
			dominated = append(dominated, candidate.engine)
		} else {
			optimal = append(optimal, candidate.engine)
		}
	}
	sort.Strings(optimal)
	sort.Strings(dominated)
	return optimal, dominated, nil
}

// GenerateReport is one ProfileReport per distinct profile among candidates, sorted by
// profile name so the report order is deterministic. higherIsBetter must name every compared
// axis: there is no default direction, because guessing wrong -- treating runtime as higher
// is better -- would silently invert the whole report. Two results for one engine in one
// profile are a *ParetoReportError.
func GenerateReport(candidates []CandidateResult, higherIsBetter map[string]bool) ([]ProfileReport, error) {
	if len(higherIsBetter) == 0 {
		return nil, fmt.Errorf("higher_is_better must name at least one metric axis")
	}
	byProfile := map[string][]CandidateResult{}
	var profiles []string
	for _, candidate := range candidates {
		if _, seen := byProfile[candidate.profile]; !seen {
			profiles = append(profiles, candidate.profile)
		}
		byProfile[candidate.profile] = append(byProfile[candidate.profile], candidate)
	}
	sort.Strings(profiles)
	reports := make([]ProfileReport, 0, len(profiles))
	for _, profile := range profiles {
		group := byProfile[profile]
		engines := map[string]bool{}
		for _, candidate := range group {
			if engines[candidate.engine] {
				return nil, &ParetoReportError{fmt.Sprintf("profile %s has more than one result for the same engine", pyrepr.String(profile))}
			}
			engines[candidate.engine] = true
		}
		optimal, dominated, err := frontier(group, higherIsBetter)
		if err != nil {
			return nil, err
		}
		winner := ""
		if len(optimal) == 1 {
			winner = optimal[0]
		}
		reports = append(reports, ProfileReport{profile, optimal, dominated, winner})
	}
	return reports, nil
}
