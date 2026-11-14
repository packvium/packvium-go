package pareto

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// The package's one job is never to collapse a trade-off into a single number. A result
// dominates another only when it is no worse on every named axis and strictly better on at
// least one, and when two results survive that, the report says so instead of picking.

// directions states both meanings, as the package requires: cost is cheaper-is-better,
// utilisation higher-is-better.
var directions = map[string]bool{"cost": false, "utilisation": true}

func candidate(t *testing.T, engine string, cost, utilisation float64, profile string) CandidateResult {
	t.Helper()
	made, err := NewCandidateResult(profile, engine, map[string]float64{"cost": cost, "utilisation": utilisation})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func balanced(t *testing.T, engine string, cost, utilisation float64) CandidateResult {
	return candidate(t, engine, cost, utilisation, "balanced")
}

func dominates(t *testing.T, a, b map[string]float64, higherIsBetter map[string]bool) bool {
	t.Helper()
	verdict, err := Dominates(a, b, higherIsBetter)
	if err != nil {
		t.Fatal(err)
	}
	return verdict
}

func report(t *testing.T, candidates ...CandidateResult) []ProfileReport {
	t.Helper()
	reports, err := GenerateReport(candidates, directions)
	if err != nil {
		t.Fatal(err)
	}
	return reports
}

func failsWith(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one mentioning %v", fragments)
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("%q does not mention %q", err, fragment)
		}
	}
}

// ------------------------------------------------------------------ dominance

func TestBetterOnBothAxesDominates(t *testing.T) {
	if !dominates(t, map[string]float64{"cost": 8, "utilisation": 0.9}, map[string]float64{"cost": 10, "utilisation": 0.7}, directions) {
		t.Fatal("not dominant")
	}
}

func TestWorseOnBothAxesDoesNot(t *testing.T) {
	if dominates(t, map[string]float64{"cost": 12, "utilisation": 0.6}, map[string]float64{"cost": 10, "utilisation": 0.7}, directions) {
		t.Fatal("dominant")
	}
}

// Cheaper but sparser: the case the package exists for. Ranking these is a claim about the
// caller's priorities that nothing in the metrics supports.
func TestAGenuineTradeOffDominatesInNeitherDirection(t *testing.T) {
	cheap := map[string]float64{"cost": 8, "utilisation": 0.6}
	dense := map[string]float64{"cost": 12, "utilisation": 0.9}
	if dominates(t, cheap, dense, directions) || dominates(t, dense, cheap, directions) {
		t.Fatal("a trade-off was ranked")
	}
}

// "No worse on every axis" holds; "strictly better somewhere" does not.
func TestIdenticalMetricsDominateInNeitherDirection(t *testing.T) {
	same := map[string]float64{"cost": 10, "utilisation": 0.8}
	if dominates(t, same, map[string]float64{"cost": 10, "utilisation": 0.8}, directions) {
		t.Fatal("dominant")
	}
}

func TestEqualOnOneAxisAndBetterOnTheOtherDominates(t *testing.T) {
	if !dominates(t, map[string]float64{"cost": 10, "utilisation": 0.9}, map[string]float64{"cost": 10, "utilisation": 0.8}, directions) {
		t.Fatal("not dominant")
	}
}

// Nothing about the numbers says which way is better, which is why the map has no default.
func TestDirectionIsReadFromTheMapRatherThanGuessed(t *testing.T) {
	low, high := map[string]float64{"cost": 8}, map[string]float64{"cost": 12}
	if !dominates(t, low, high, map[string]bool{"cost": false}) || !dominates(t, high, low, map[string]bool{"cost": true}) {
		t.Fatal("direction ignored")
	}
}

func TestAnAxisMissingFromEitherSideIsRefused(t *testing.T) {
	for _, pair := range [][2]map[string]float64{
		{{"cost": 8}, {"cost": 10, "utilisation": 0.7}},
		{{"cost": 8, "utilisation": 0.9}, {"cost": 10}},
	} {
		_, err := Dominates(pair[0], pair[1], directions)
		var inconsistent *InconsistentAxesError
		if !errors.As(err, &inconsistent) || err.Error() != "missing metric axis/axes: ['utilisation']" {
			t.Fatalf("got %v", err)
		}
	}
}

// Extra metrics are not an error; they are data the caller did not ask to rank on.
func TestAnAxisAbsentFromTheDirectionMapIsSimplyNotCompared(t *testing.T) {
	if !dominates(t, map[string]float64{"cost": 8, "weight": 99}, map[string]float64{"cost": 10, "weight": 1}, map[string]bool{"cost": false}) {
		t.Fatal("not dominant")
	}
}

// NaN > x and NaN < x are both false, so an unguarded comparison silently reports a tie.
func TestANaNOnEitherSideIsRefusedRatherThanCountedEqual(t *testing.T) {
	nan := math.NaN()
	_, err := Dominates(map[string]float64{"cost": nan}, map[string]float64{"cost": 1}, map[string]bool{"cost": false})
	var nonFinite *NonFiniteMetricError
	if !errors.As(err, &nonFinite) || err.Error() != "left candidate: metric 'cost' is NaN and cannot be compared" {
		t.Fatalf("got %v", err)
	}
	_, err = Dominates(map[string]float64{"cost": 1}, map[string]float64{"cost": nan}, map[string]bool{"cost": false})
	failsWith(t, err, "right candidate", "cost")
}

// Deliberately allowed: a caller encoding "unpriceable" as an infinite cost gets the answer
// they mean.
func TestInfinitiesCompareAsTheEndsOfTheNumberLine(t *testing.T) {
	inf := math.Inf(1)
	cost := map[string]bool{"cost": false}
	if !dominates(t, map[string]float64{"cost": 5}, map[string]float64{"cost": inf}, cost) ||
		dominates(t, map[string]float64{"cost": inf}, map[string]float64{"cost": 5}, cost) ||
		dominates(t, map[string]float64{"cost": inf}, map[string]float64{"cost": inf}, cost) {
		t.Fatal("infinity misordered")
	}
}

func TestTheSubclassesAreParetoReportErrors(t *testing.T) {
	var base *ParetoReportError
	if !errors.As(&InconsistentAxesError{"x"}, &base) || !errors.As(&NonFiniteMetricError{"x"}, &base) {
		t.Fatal("not a ParetoReportError")
	}
}

// ------------------------------------------------------------------ the report

func TestOneDominantCandidateBecomesTheNamedWinner(t *testing.T) {
	reports := report(t, balanced(t, "python", 10, 0.7), balanced(t, "php", 8, 0.9))
	if len(reports) != 1 || reports[0].Winner() != "php" ||
		!reflect.DeepEqual(reports[0].ParetoOptimal(), []string{"php"}) || !reflect.DeepEqual(reports[0].Dominated(), []string{"python"}) {
		t.Fatalf("%+v", reports)
	}
}

func TestATradeOffLeavesNoWinnerAndListsBoth(t *testing.T) {
	reports := report(t, balanced(t, "python", 8, 0.6), balanced(t, "php", 12, 0.9))
	if reports[0].Winner() != "" || !reflect.DeepEqual(reports[0].ParetoOptimal(), []string{"php", "python"}) || len(reports[0].Dominated()) != 0 {
		t.Fatalf("%+v", reports[0])
	}
}

// Nothing can dominate a lone candidate, so it is on the frontier; easy to misread as
// evidence that it beat something.
func TestASingleCandidateIsOptimalByDefault(t *testing.T) {
	reports := report(t, balanced(t, "python", 10, 0.7))
	if !reflect.DeepEqual(reports[0].ParetoOptimal(), []string{"python"}) || reports[0].Winner() != "python" {
		t.Fatalf("%+v", reports[0])
	}
}

func TestThreeCandidatesSplitIntoFrontierAndDominated(t *testing.T) {
	reports := report(t, balanced(t, "cheap", 8, 0.6), balanced(t, "dense", 12, 0.9), balanced(t, "worse", 14, 0.5))
	if !reflect.DeepEqual(reports[0].ParetoOptimal(), []string{"cheap", "dense"}) ||
		!reflect.DeepEqual(reports[0].Dominated(), []string{"worse"}) || reports[0].Winner() != "" {
		t.Fatalf("%+v", reports[0])
	}
}

func TestEachProfileGetsItsOwnReportSortedByName(t *testing.T) {
	reports := report(t, candidate(t, "python", 10, 0.7, "quality"), candidate(t, "php", 8, 0.9, "fast"))
	if len(reports) != 2 || reports[0].Profile() != "fast" || reports[1].Profile() != "quality" {
		t.Fatalf("%+v", reports)
	}
}

// Report order cannot depend on input order, or two runs of one comparison would print
// differently.
func TestFrontierAndDominatedAreEachSorted(t *testing.T) {
	forward := report(t, balanced(t, "zeta", 8, 0.6), balanced(t, "alpha", 12, 0.9))
	reverse := report(t, balanced(t, "alpha", 12, 0.9), balanced(t, "zeta", 8, 0.6))
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(forward[0].ParetoOptimal(), want) || !reflect.DeepEqual(reverse[0].ParetoOptimal(), want) {
		t.Fatalf("%v %v", forward[0].ParetoOptimal(), reverse[0].ParetoOptimal())
	}
}

func TestNoCandidatesProducesNoReports(t *testing.T) {
	if reports := report(t); len(reports) != 0 {
		t.Fatalf("%+v", reports)
	}
}

func TestTwoResultsForOneEngineInOneProfileAreRefused(t *testing.T) {
	_, err := GenerateReport([]CandidateResult{balanced(t, "python", 10, 0.7), balanced(t, "python", 8, 0.9)}, directions)
	var refusal *ParetoReportError
	if !errors.As(err, &refusal) || err.Error() != "profile 'balanced' has more than one result for the same engine" {
		t.Fatalf("got %v", err)
	}
}

// With no axes there is no dominance, so every candidate would be "optimal".
func TestAnEmptyDirectionMapIsRefused(t *testing.T) {
	_, err := GenerateReport([]CandidateResult{balanced(t, "python", 10, 0.7)}, map[string]bool{})
	failsWith(t, err, "at least one metric axis")
}

// Measured before the refusal existed: an all-NaN candidate came back optimal beside a clean
// one, because nothing could dominate it.
func TestANaNCandidateCannotReachTheFrontier(t *testing.T) {
	_, err := NewCandidateResult("balanced", "broken", map[string]float64{"cost": math.NaN(), "utilisation": math.NaN()})
	var nonFinite *NonFiniteMetricError
	if !errors.As(err, &nonFinite) || err.Error() != "broken on profile balanced: metric 'cost' is NaN and cannot be compared" {
		t.Fatalf("got %v", err)
	}
}

func TestACandidateNeedsAProfileAnEngineAndMetrics(t *testing.T) {
	cases := []struct {
		profile, engine string
		metrics         map[string]float64
		message         string
	}{
		{"", "python", map[string]float64{"cost": 1}, "profile is required"},
		{"fast", "", map[string]float64{"cost": 1}, "engine is required"},
		{"fast", "python", nil, "metrics cannot be empty"},
	}
	for _, c := range cases {
		if _, err := NewCandidateResult(c.profile, c.engine, c.metrics); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}

// A candidate keeps its own copy: changing the caller's map afterwards changes nothing.
func TestACandidatesMetricsCannotBeChangedFromOutside(t *testing.T) {
	metrics := map[string]float64{"cost": 1}
	made, err := NewCandidateResult("fast", "python", metrics)
	if err != nil {
		t.Fatal(err)
	}
	metrics["cost"] = 2
	made.Metrics()["cost"] = 3
	if made.Metrics()["cost"] != 1 {
		t.Fatal("the candidate's metrics changed")
	}
}

// ---------------------------------------------------- the report type guards its claims

func TestAWinnerMustBeOnTheFrontier(t *testing.T) {
	_, err := NewProfileReport("fast", []string{"php"}, []string{"python"}, "python")
	failsWith(t, err, "must be one of the Pareto-optimal")
}

func TestAWinnerCannotBeNamedWhenTwoSurvive(t *testing.T) {
	_, err := NewProfileReport("fast", []string{"php", "python"}, nil, "php")
	failsWith(t, err, "exactly one candidate")
}

func TestNoWinnerAlongsideAFrontierOfTwoIsWellFormed(t *testing.T) {
	made, err := NewProfileReport("fast", []string{"php", "python"}, nil, "")
	if err != nil || made.Winner() != "" {
		t.Fatalf("%v %v", made, err)
	}
}
