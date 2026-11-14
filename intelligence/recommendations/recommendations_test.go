package recommendations

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/commerce"
	"github.com/packvium/packvium-go/intelligence/simulation"
)

// Two properties carry the package. A proposal is built from a paired comparison, so an order
// only one arm could pack contributes to neither mean and cannot manufacture an improvement
// out of cohort mix. And a Recommendation is never itself a change: it is handed no registry,
// and the only path to one is an explicit approval call.

var (
	constraints = []string{"no placement the validator rejects"}
	rollback    = "republish catalog version 1"
)

// order is one order's (cost, utilisation) in an arm; a nil entry is an order the arm could
// not pack.
type order struct {
	id       string
	measured *[2]float64
}

func m(id string, cost, utilisation float64) order { return order{id, &[2]float64{cost, utilisation}} }
func failed(id string) order                       { return order{id, nil} }

func pin(t *testing.T) simulation.ScenarioVersionPin {
	t.Helper()
	made, err := simulation.NewScenarioVersionPin(simulation.VersionPinSpec{CatalogVersion: simulation.Version(1)})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func runOf(t *testing.T, id string, metrics map[string]float64) simulation.OrderRunResult {
	t.Helper()
	spec := simulation.OrderRunSpec{OrderID: id, Succeeded: metrics != nil, Metrics: metrics}
	if metrics == nil {
		spec.FailureReason = "no carton fits"
	}
	made, err := simulation.NewOrderRunResult(spec)
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func scenarioOf(t *testing.T, name string, runs ...simulation.OrderRunResult) simulation.ScenarioResult {
	t.Helper()
	ids := make([]string, len(runs))
	for index, run := range runs {
		ids[index] = run.OrderID()
	}
	made, err := simulation.NewScenarioResult(name, pin(t), ids, runs)
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func scenario(t *testing.T, name string, orders ...order) simulation.ScenarioResult {
	t.Helper()
	runs := make([]simulation.OrderRunResult, len(orders))
	for index, entry := range orders {
		var metrics map[string]float64
		if entry.measured != nil {
			metrics = map[string]float64{"cost": entry.measured[0], "utilisation": entry.measured[1]}
		}
		runs[index] = runOf(t, entry.id, metrics)
	}
	return scenarioOf(t, name, runs...)
}

func evidence() Evidence {
	return Evidence{Constraints: constraints, RollbackPlan: rollback, MinimumCohortSize: 1, MinimumConfidence: 0}
}

func propose(t *testing.T, baseline, treatment simulation.ScenarioResult, bar Evidence) *Recommendation {
	t.Helper()
	made, err := Propose("rec-1", "cheaper cartons", baseline, treatment, bar)
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func means(proposal *Recommendation) map[string][2]float64 {
	byMetric := map[string][2]float64{}
	for _, delta := range proposal.ExpectedDeltas() {
		byMetric[delta.Metric()] = [2]float64{delta.BaselineMean(), delta.TreatmentMean()}
	}
	return byMetric
}

// ------------------------------------------- a proposal is built from paired evidence

func TestTheDeltasAreTheMeansOfThePairedOrders(t *testing.T) {
	proposal := propose(t, scenario(t, "b", m("a", 10, 0.6), m("b", 20, 0.8)), scenario(t, "t", m("a", 8, 0.7), m("b", 16, 0.9)), evidence())
	if proposal == nil {
		t.Fatal("suppressed")
	}
	got := means(proposal)
	if got["cost"] != [2]float64{15, 12} || got["utilisation"] != [2]float64{0.7, 0.8} {
		t.Fatalf("%v", got)
	}
}

// Report order cannot depend on map order, or two identical comparisons would print
// differently.
func TestDeltasAreSortedByMetricName(t *testing.T) {
	proposal := propose(t, scenario(t, "b", m("a", 10, 0.6)), scenario(t, "t", m("a", 8, 0.7)), evidence())
	deltas := proposal.ExpectedDeltas()
	if len(deltas) != 2 || deltas[0].Metric() != "cost" || deltas[1].Metric() != "utilisation" {
		t.Fatalf("%v", deltas)
	}
}

// The paired rule: averaging each arm's survivors separately would let the difference in who
// survived look like a difference in performance.
func TestAnOrderOnlyOneArmPackedIsExcludedFromBothMeans(t *testing.T) {
	proposal := propose(t, scenario(t, "b", m("a", 10, 0.6), m("b", 100, 0.1)), scenario(t, "t", m("a", 8, 0.7), failed("b")), evidence())
	if !reflect.DeepEqual(proposal.SupportingOrderIDs(), []string{"a"}) || means(proposal)["cost"][0] != 10 {
		t.Fatalf("%v %v", proposal.SupportingOrderIDs(), means(proposal))
	}
}

func TestConfidenceIsThePairedShareOfTheWholeCorpus(t *testing.T) {
	proposal := propose(t,
		scenario(t, "b", m("a", 10, 0.6), m("b", 10, 0.6), m("c", 10, 0.6), m("d", 10, 0.6)),
		scenario(t, "t", m("a", 8, 0.7), m("b", 8, 0.7), m("c", 8, 0.7), failed("d")), evidence())
	if proposal.Confidence() != 0.75 {
		t.Fatalf("%v", proposal.Confidence())
	}
}

func TestOnlyMetricsBothArmsReportedBecomeDeltas(t *testing.T) {
	baseline := scenarioOf(t, "b", runOf(t, "a", map[string]float64{"cost": 10, "weight": 5}))
	treatment := scenarioOf(t, "t", runOf(t, "a", map[string]float64{"cost": 8, "volume": 3}))
	deltas := propose(t, baseline, treatment, evidence()).ExpectedDeltas()
	if len(deltas) != 1 || deltas[0].Metric() != "cost" {
		t.Fatalf("%v", deltas)
	}
}

// --------------------------------------- a proposal is withheld rather than hedged

func TestACohortBelowTheFloorProducesNothing(t *testing.T) {
	bar := evidence()
	bar.MinimumCohortSize = 2
	if propose(t, scenario(t, "b", m("a", 10, 0.6)), scenario(t, "t", m("a", 8, 0.7)), bar) != nil {
		t.Fatal("proposed")
	}
}

func TestAConfidenceBelowTheFloorProducesNothing(t *testing.T) {
	bar := evidence()
	bar.MinimumConfidence = 0.9
	if propose(t, scenario(t, "b", m("a", 10, 0.6), m("b", 10, 0.6)), scenario(t, "t", m("a", 8, 0.7), failed("b")), bar) != nil {
		t.Fatal("proposed")
	}
}

// Full cohort, full confidence, and still no axis both sides measured: nil rather than a
// recommendation carrying an empty delta list.
func TestArmsThatShareNoMetricProduceNothing(t *testing.T) {
	baseline := scenarioOf(t, "b", runOf(t, "a", map[string]float64{"cost": 10}))
	treatment := scenarioOf(t, "t", runOf(t, "a", map[string]float64{"utilisation": 0.9}))
	if propose(t, baseline, treatment, evidence()) != nil {
		t.Fatal("proposed")
	}
}

func TestTwoDifferentCorporaAreRefused(t *testing.T) {
	_, err := Propose("rec-1", "p", scenario(t, "b", m("a", 10, 0.6)), scenario(t, "t", m("z", 8, 0.7)), evidence())
	var mismatch *simulation.MismatchedOrderCorpusError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v", err)
	}
}

func TestTheEvidenceBarMustBeStated(t *testing.T) {
	base, treat := scenario(t, "b", m("a", 10, 0.6)), scenario(t, "t", m("a", 8, 0.7))
	bar := evidence()
	bar.MinimumCohortSize = 0
	if _, err := Propose("rec-1", "p", base, treat, bar); err == nil || err.Error() != "minimum_cohort_size must be positive" {
		t.Fatalf("got %v", err)
	}
	bar = evidence()
	bar.MinimumConfidence = 1.5
	if _, err := Propose("rec-1", "p", base, treat, bar); err == nil || err.Error() != "minimum_confidence must be between 0 and 1" {
		t.Fatalf("got %v", err)
	}
}

// --------------------------------------- publishing is a separate, explicit act

func recommendation(t *testing.T) Recommendation {
	t.Helper()
	delta, err := NewExpectedDelta("cost", 10, 8)
	if err != nil {
		t.Fatal(err)
	}
	made, err := NewRecommendation(RecommendationSpec{
		RecommendationID: "rec-1", Proposal: "cheaper cartons", SupportingOrderIDs: []string{"a"},
		ExpectedDeltas: []ExpectedDelta{delta}, Confidence: 1, Constraints: constraints, RollbackPlan: rollback,
	})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func TestApproveCallsTheInjectedPublisherExactlyOnce(t *testing.T) {
	calls := 0
	record, err := Approve(recommendation(t), 1000, func() (int64, error) {
		calls++
		return 7, nil
	})
	want, _ := NewApprovalRecord("rec-1", 1000, 7)
	if err != nil || calls != 1 || record != want {
		t.Fatalf("%v %d %v", record, calls, err)
	}
}

func TestAPublisherErrorIsNotRecordedAsAnApproval(t *testing.T) {
	failure := errors.New("registry unavailable")
	if _, err := Approve(recommendation(t), 1000, func() (int64, error) { return 0, failure }); err != failure {
		t.Fatalf("got %v", err)
	}
}

func catalogRegistry(t *testing.T) *commerce.CatalogRegistry {
	t.Helper()
	registry, err := commerce.NewCatalogRegistry("cartons")
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestACatalogApprovalPublishesThroughTheRealRegistry(t *testing.T) {
	record, err := ApproveCatalog(recommendation(t), catalogRegistry(t), &commerce.CatalogSnapshot{}, 1000, 2000)
	if err != nil || record.PublishedVersion() != 1 || record.RecommendationID() != "rec-1" {
		t.Fatalf("%v %v", record, err)
	}
}

// Append-only: publishing again never replaces what was published before.
func TestASecondCatalogApprovalAppendsANewVersion(t *testing.T) {
	registry := catalogRegistry(t)
	first, err := ApproveCatalog(recommendation(t), registry, &commerce.CatalogSnapshot{}, 1000, 2000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ApproveCatalog(recommendation(t), registry, &commerce.CatalogSnapshot{}, 3000, 4000)
	if err != nil || first.PublishedVersion() != 1 || second.PublishedVersion() != 2 {
		t.Fatalf("%v %v %v", first, second, err)
	}
}

func TestAPolicyApprovalPublishesAVersionedRule(t *testing.T) {
	predicate, err := commerce.NewPolicyPredicate(commerce.ScopeHazmat, "class", commerce.OperatorEquals, "1.4")
	if err != nil {
		t.Fatal(err)
	}
	record, err := ApprovePolicy(recommendation(t), commerce.NewPolicyRegistry(), PolicyChange{
		RuleID: "no-hazmat-air", Scope: commerce.ScopeHazmat, Action: commerce.ActionReject,
		Predicates: []commerce.PolicyPredicate{predicate}, Priority: 10, EffectiveAt: 2000,
	}, 1000)
	if err != nil || record.PublishedVersion() != 1 || record.RecommendationID() != "rec-1" {
		t.Fatalf("%v %v", record, err)
	}
}

// The registry still refuses what it would refuse from a document: the approval path is not
// a way around admission.
func TestAPolicyApprovalOutsideTheVocabularyIsRefused(t *testing.T) {
	_, err := commerce.NewPolicyPredicate("weather", "rain", commerce.OperatorExists, nil)
	if err == nil || err.Error() != "unsupported policy scope 'weather'" {
		t.Fatalf("got %v", err)
	}
	handmade := commerce.PolicyPredicate{Scope: commerce.ScopeHazmat, Field: "class", Operator: "resembles", Value: "1"}
	_, err = ApprovePolicy(recommendation(t), commerce.NewPolicyRegistry(), PolicyChange{
		RuleID: "odd", Scope: commerce.ScopeHazmat, Action: commerce.ActionReject,
		Predicates: []commerce.PolicyPredicate{handmade}, Priority: 1, EffectiveAt: 0,
	}, 1000)
	if err == nil || err.Error() != "rule 'odd' uses an unsupported scope/operator combination" {
		t.Fatalf("got %v", err)
	}
}

// Structural, not a convention: Propose takes no registry, so the proposal path cannot reach
// a change even by mistake.
func TestAProposalIsHandedNoRegistryOfItsOwn(t *testing.T) {
	signature := reflect.TypeOf(Propose)
	for index := 0; index < signature.NumIn(); index++ {
		if name := signature.In(index).String(); strings.Contains(strings.ToLower(name), "registry") {
			t.Errorf("Propose takes a %s", name)
		}
	}
}

// --------------------------------------------------------- the delta itself

// Signed on purpose: whether a negative delta is good depends on the axis.
func TestDeltaIsTreatmentMinusBaseline(t *testing.T) {
	cheaper, _ := NewExpectedDelta("cost", 10, 8)
	denser, _ := NewExpectedDelta("utilisation", 0.7, 0.9)
	if cheaper.Delta() != -2 || math.Abs(denser.Delta()-0.2) > 1e-12 {
		t.Fatalf("%v %v", cheaper.Delta(), denser.Delta())
	}
}

// A proposal with nothing measured is advice without evidence.
func TestARecommendationMustReportAtLeastOneDelta(t *testing.T) {
	_, err := NewRecommendation(RecommendationSpec{RecommendationID: "rec-1", Proposal: "p", SupportingOrderIDs: []string{"a"},
		Confidence: 1, Constraints: constraints, RollbackPlan: rollback})
	if err == nil || !strings.Contains(err.Error(), "at least one expected delta") {
		t.Fatalf("got %v", err)
	}
}

func TestARecommendationGuardsEveryOtherClaim(t *testing.T) {
	delta, _ := NewExpectedDelta("cost", 10, 8)
	valid := RecommendationSpec{RecommendationID: "rec-1", Proposal: "p", SupportingOrderIDs: []string{"a"},
		ExpectedDeltas: []ExpectedDelta{delta}, Confidence: 1, Constraints: constraints, RollbackPlan: rollback}
	cases := []struct {
		change  func(*RecommendationSpec)
		message string
	}{
		{func(s *RecommendationSpec) { s.RecommendationID = "" }, "recommendation_id is required"},
		{func(s *RecommendationSpec) { s.Proposal = "" }, "proposal is required"},
		{func(s *RecommendationSpec) { s.SupportingOrderIDs = nil }, "a recommendation must cite at least one supporting order"},
		{func(s *RecommendationSpec) { s.Confidence = math.NaN() }, "confidence must be between 0 and 1"},
		{func(s *RecommendationSpec) { s.Constraints = []string{""} }, "a recommendation must state at least one non-empty constraint"},
		{func(s *RecommendationSpec) { s.RollbackPlan = "" }, "a recommendation must state its rollback plan"},
	}
	for _, c := range cases {
		spec := valid
		c.change(&spec)
		if _, err := NewRecommendation(spec); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
	if _, err := NewExpectedDelta("", 1, 2); err == nil || err.Error() != "metric is required" {
		t.Errorf("got %v", err)
	}
	for _, c := range []struct {
		id          string
		at, version int64
		message     string
	}{
		{"", 0, 1, "recommendation_id is required"},
		{"r", -1, 1, "approved_at cannot be negative"},
		{"r", 0, 0, "published_version must be positive"},
	} {
		if _, err := NewApprovalRecord(c.id, c.at, c.version); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}
