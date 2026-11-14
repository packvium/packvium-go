package simulation

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/intelligence/pareto"
)

// A scenario is compared per order, never as a blended delta: an aggregate would hide which
// orders improved and which paid for the improvement, exactly what a caller needs to know
// before publishing a catalog change.

var directions = map[string]bool{"cost": false, "utilisation": true}

func pin(t *testing.T, catalogVersion int64) ScenarioVersionPin {
	t.Helper()
	made, err := NewScenarioVersionPin(VersionPinSpec{CatalogVersion: Version(catalogVersion)})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

// measured is one arm's (cost, utilisation) for an order; nil means the arm could not pack it.
type measured *[2]float64

func m(cost, utilisation float64) measured { return &[2]float64{cost, utilisation} }

// arm is an evaluator that replays a table.
func arm(t *testing.T, table map[string]measured) OrderEvaluator {
	return func(orderID string, _ ScenarioVersionPin) (OrderRunResult, error) {
		values := table[orderID]
		if values == nil {
			return NewOrderRunResult(OrderRunSpec{OrderID: orderID, FailureReason: "no carton fits"})
		}
		return NewOrderRunResult(OrderRunSpec{OrderID: orderID, Succeeded: true,
			Metrics: map[string]float64{"cost": values[0], "utilisation": values[1]}})
	}
}

func run(t *testing.T, name string, orders []string, table map[string]measured) ScenarioResult {
	t.Helper()
	result, err := RunScenario(name, pin(t, 1), orders, arm(t, table))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func compare(t *testing.T, baseline, treatment ScenarioResult) map[string]pareto.ProfileReport {
	t.Helper()
	reports, err := CompareScenarios(baseline, treatment, directions)
	if err != nil {
		t.Fatal(err)
	}
	byOrder := map[string]pareto.ProfileReport{}
	for _, report := range reports {
		byOrder[report.Profile()] = report
	}
	return byOrder
}

func oneMetric(orderID string, _ ScenarioVersionPin) (OrderRunResult, error) {
	return NewOrderRunResult(OrderRunSpec{OrderID: orderID, Succeeded: true, Metrics: map[string]float64{"cost": 1}})
}

func wantError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("got %v, want an error mentioning %q", err, fragment)
	}
}

// ------------------------------------------------------------- running a scenario

func TestEveryOrderIsEvaluatedInTheGivenSequence(t *testing.T) {
	var seen []string
	result, err := RunScenario("s", pin(t, 1), []string{"b", "a", "c"}, func(orderID string, p ScenarioVersionPin) (OrderRunResult, error) {
		seen = append(seen, orderID)
		return oneMetric(orderID, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []string{"b", "a", "c"}) || !reflect.DeepEqual(result.OrderIDs(), []string{"b", "a", "c"}) {
		t.Fatalf("%v %v", seen, result.OrderIDs())
	}
}

// A scenario replays from stored versions, so the pin is what the evaluator resolves against.
func TestThePinReachesTheEvaluatorUnchanged(t *testing.T) {
	var seen []ScenarioVersionPin
	_, err := RunScenario("s", pin(t, 1), []string{"a"}, func(orderID string, p ScenarioVersionPin) (OrderRunResult, error) {
		seen = append(seen, p)
		return oneMetric(orderID, p)
	})
	if err != nil || len(seen) != 1 || !seen[0].Equal(pin(t, 1)) {
		t.Fatalf("%v %v", seen, err)
	}
}

// Reproducibility is this function's own determinism, not a separate mechanism.
func TestTheSameArgumentsReproduceAnEqualResult(t *testing.T) {
	table := map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)}
	if !reflect.DeepEqual(run(t, "s", []string{"a", "b"}, table), run(t, "s", []string{"a", "b"}, table)) {
		t.Fatal("two identical runs differ")
	}
}

func TestAFailedOrderIsCarriedRatherThanDropped(t *testing.T) {
	result := run(t, "s", []string{"a", "b"}, map[string]measured{"a": m(10, 0.7), "b": nil})
	runs := result.Runs()
	if !runs[0].Succeeded() || runs[1].Succeeded() || runs[1].FailureReason() != "no carton fits" {
		t.Fatalf("%+v", runs)
	}
}

func TestAnEvaluatorErrorStopsTheScenario(t *testing.T) {
	failure := errors.New("the packer refused the request")
	_, err := RunScenario("s", pin(t, 1), []string{"a"}, func(string, ScenarioVersionPin) (OrderRunResult, error) {
		return OrderRunResult{}, failure
	})
	if err != failure {
		t.Fatalf("got %v", err)
	}
}

func TestMetricsAreReadOnlyOnceTheRunExists(t *testing.T) {
	metrics := map[string]float64{"cost": 1}
	made, err := NewOrderRunResult(OrderRunSpec{OrderID: "a", Succeeded: true, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	metrics["cost"] = 2
	made.Metrics()["cost"] = 3
	if value, _ := made.Metric("cost"); value != 1 {
		t.Fatal("the run's metrics changed")
	}
}

// ------------------------------------------------------- a run guards its own claims

// "It worked" with nothing measured is not a comparable result.
func TestASucceededRunMustReportAMetric(t *testing.T) {
	_, err := NewOrderRunResult(OrderRunSpec{OrderID: "a", Succeeded: true, Metrics: map[string]float64{}})
	wantError(t, err, "at least one metric")
}

func TestAFailedRunMustSayWhy(t *testing.T) {
	_, err := NewOrderRunResult(OrderRunSpec{OrderID: "a"})
	wantError(t, err, "failure_reason")
}

func TestARunNeedsAnOrderAndNamedMetrics(t *testing.T) {
	_, err := NewOrderRunResult(OrderRunSpec{Succeeded: true, Metrics: map[string]float64{"cost": 1}})
	wantError(t, err, "order_id is required")
	_, err = NewOrderRunResult(OrderRunSpec{OrderID: "a", Succeeded: true, Metrics: map[string]float64{"": 1}})
	wantError(t, err, "metric names must be non-empty")
}

// A measurement of infinite cost is a broken evaluator, not a caller's encoding of
// "unpriceable"; the reference also refuses a boolean, which a float64 cannot be.
func TestANonFiniteMetricIsRefusedAtTheRun(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := NewOrderRunResult(OrderRunSpec{OrderID: "a", Succeeded: true, Metrics: map[string]float64{"cost": value}})
		wantError(t, err, "finite")
	}
}

// ------------------------------------------------------- comparing two scenarios

func pair(t *testing.T, baseline, treatment map[string]measured) (ScenarioResult, ScenarioResult) {
	orders := []string{"a", "b"}
	return run(t, "base", orders, baseline), run(t, "treat", orders, treatment)
}

func compared(t *testing.T, baseline, treatment map[string]measured) map[string]pareto.ProfileReport {
	t.Helper()
	base, treat := pair(t, baseline, treatment)
	return compare(t, base, treat)
}

func TestOneReportPerOrder(t *testing.T) {
	base, treat := pair(t, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)}, map[string]measured{"a": m(8, 0.9), "b": m(11, 0.85)})
	reports, err := CompareScenarios(base, treat, directions)
	if err != nil || len(reports) != 2 || reports[0].Profile() != "a" || reports[1].Profile() != "b" {
		t.Fatalf("%+v %v", reports, err)
	}
}

func TestADominantArmIsNamedPerOrder(t *testing.T) {
	reports := compared(t, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)}, map[string]measured{"a": m(8, 0.9), "b": m(14, 0.6)})
	if reports["a"].Winner() != "treatment" || reports["b"].Winner() != "baseline" {
		t.Fatalf("%+v", reports)
	}
}

// Order b genuinely trades cost for density, and no single number can report that without
// deciding it.
func TestARealTradeOffOnOneOrderLeavesThatOrderWithoutAWinner(t *testing.T) {
	reports := compared(t, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)}, map[string]measured{"a": m(8, 0.9), "b": m(14, 0.9)})
	if reports["a"].Winner() != "treatment" || reports["b"].Winner() != "" ||
		!reflect.DeepEqual(reports["b"].ParetoOptimal(), []string{"baseline", "treatment"}) {
		t.Fatalf("%+v", reports)
	}
}

// Named winner, and not a win: there was only one candidate to be optimal.
func TestAnOrderOnlyOneArmCouldPackReportsThatArmAlone(t *testing.T) {
	reports := compared(t, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)}, map[string]measured{"a": m(8, 0.9), "b": nil})
	if !reflect.DeepEqual(reports["b"].ParetoOptimal(), []string{"baseline"}) || reports["b"].Winner() != "baseline" {
		t.Fatalf("%+v", reports["b"])
	}
}

func TestAnOrderNeitherArmCouldPackProducesNoReport(t *testing.T) {
	reports := compared(t, map[string]measured{"a": m(10, 0.7), "b": nil}, map[string]measured{"a": m(8, 0.9), "b": nil})
	if _, present := reports["b"]; present || len(reports) != 1 {
		t.Fatalf("%+v", reports)
	}
}

// Comparing the overlap would let cohort mix manufacture a difference neither arm produced.
func TestTwoDifferentCorporaAreRefusedRatherThanIntersected(t *testing.T) {
	base := run(t, "base", []string{"a", "b"}, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)})
	treat := run(t, "treat", []string{"a", "c"}, map[string]measured{"a": m(8, 0.9), "c": m(9, 0.9)})
	_, err := CompareScenarios(base, treat, directions)
	var mismatch *MismatchedOrderCorpusError
	var scenario *ScenarioError
	if !errors.As(err, &mismatch) || !errors.As(err, &scenario) || !strings.Contains(err.Error(), "identical order corpus") {
		t.Fatalf("got %v", err)
	}
}

func TestTheSameOrdersInADifferentSequenceAreRefused(t *testing.T) {
	base := run(t, "base", []string{"a", "b"}, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)})
	treat := run(t, "treat", []string{"b", "a"}, map[string]measured{"a": m(8, 0.9), "b": m(11, 0.9)})
	_, err := CompareScenarios(base, treat, directions)
	var mismatch *MismatchedOrderCorpusError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v", err)
	}
}

// The point of a comparison: one pin per arm, the same orders.
func TestTheArmsMayBePinnedToDifferentCatalogVersions(t *testing.T) {
	base := run(t, "base", []string{"a"}, map[string]measured{"a": m(10, 0.7)})
	better, err := NewOrderRunResult(OrderRunSpec{OrderID: "a", Succeeded: true, Metrics: map[string]float64{"cost": 8, "utilisation": 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	treat, err := NewScenarioResult("treat", pin(t, 2), []string{"a"}, []OrderRunResult{better})
	if err != nil {
		t.Fatal(err)
	}
	if compare(t, base, treat)["a"].Winner() != "treatment" {
		t.Fatal("the treatment did not win")
	}
}

// ---------------------------------------------- a scenario reports its own confidence

func mixed(t *testing.T) ScenarioResult {
	return run(t, "s", []string{"a", "b", "c", "d"}, map[string]measured{"a": m(10, 0.7), "b": nil, "c": m(12, 0.8), "d": m(9, 0.9)})
}

func TestTheCountsPartitionTheRuns(t *testing.T) {
	result := mixed(t)
	if result.SucceededCount() != 3 || result.FailedCount() != 1 || result.SucceededCount()+result.FailedCount() != len(result.Runs()) {
		t.Fatalf("%d %d", result.SucceededCount(), result.FailedCount())
	}
}

func TestConfidenceIsTheUsableShare(t *testing.T) {
	if confidence := mixed(t).Confidence(); confidence != 0.75 {
		t.Fatalf("%v", confidence)
	}
}

func TestAWhollySuccessfulScenarioHasFullConfidence(t *testing.T) {
	result := run(t, "s", []string{"a"}, map[string]measured{"a": m(10, 0.7)})
	if result.Confidence() != 1 || result.FailedCount() != 0 {
		t.Fatalf("%v %d", result.Confidence(), result.FailedCount())
	}
}

func TestAWhollyFailedScenarioHasNoConfidence(t *testing.T) {
	result := run(t, "s", []string{"a"}, map[string]measured{"a": nil})
	if result.Confidence() != 0 || result.SucceededCount() != 0 {
		t.Fatalf("%v %d", result.Confidence(), result.SucceededCount())
	}
}

// -------------------------------------------------- reaching one order's raw run

func TestTheRunForAKnownOrderComesBack(t *testing.T) {
	result := run(t, "s", []string{"a", "b"}, map[string]measured{"a": m(10, 0.7), "b": m(12, 0.8)})
	found, err := result.RawArtifact("b")
	if cost, _ := found.Metric("cost"); err != nil || cost != 12 {
		t.Fatalf("%v %v", cost, err)
	}
}

// Returning an empty run would push the mistake downstream into a metrics lookup.
func TestAnUnknownOrderIsRefusedByName(t *testing.T) {
	_, err := run(t, "s", []string{"a"}, map[string]measured{"a": m(10, 0.7)}).RawArtifact("zzz")
	var scenario *ScenarioError
	if !errors.As(err, &scenario) || err.Error() != "no run recorded for order 'zzz'" {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------------- a result and a pin guard their claims

func TestARunsMustCorrespondToTheOrders(t *testing.T) {
	only, _ := oneMetric("a", ScenarioVersionPin{})
	_, err := NewScenarioResult("s", pin(t, 1), []string{"b"}, []OrderRunResult{only})
	wantError(t, err, "runs must correspond 1:1 to order_ids, in the same order")
	_, err = NewScenarioResult("", pin(t, 1), []string{"a"}, []OrderRunResult{only})
	wantError(t, err, "scenario_id is required")
	_, err = NewScenarioResult("s", pin(t, 1), nil, nil)
	wantError(t, err, "order_ids cannot be empty")
}

func TestAPinMustSayWhatItPins(t *testing.T) {
	empty := ""
	cases := []struct {
		spec    VersionPinSpec
		message string
	}{
		{VersionPinSpec{}, "a version pin must set at least one of its fields"},
		{VersionPinSpec{CatalogVersion: Version(0)}, "version numbers must be positive"},
		{VersionPinSpec{PolicyVersions: []PolicyVersion{{"", 1}}}, "policy version pins require a rule id and positive version"},
		{VersionPinSpec{PolicyVersions: []PolicyVersion{{"r", 1}, {"r", 2}}}, "a version pin cannot name the same policy rule twice"},
		{VersionPinSpec{SolverVersion: &empty}, "solver_version must be non-empty when supplied"},
	}
	for _, c := range cases {
		if _, err := NewScenarioVersionPin(c.spec); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}

func TestPolicyPinsAreKeptSortedAndPinsCompareByValue(t *testing.T) {
	first, err := NewScenarioVersionPin(VersionPinSpec{PolicyVersions: []PolicyVersion{{"b", 1}, {"a", 2}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewScenarioVersionPin(VersionPinSpec{PolicyVersions: []PolicyVersion{{"a", 2}, {"b", 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.PolicyVersions(), []PolicyVersion{{"a", 2}, {"b", 1}}) || !first.Equal(second) || first.Equal(pin(t, 1)) {
		t.Fatalf("%v", first.PolicyVersions())
	}
}
