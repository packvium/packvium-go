package holdout

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/packvium/packvium-go/intelligence/outcomes"
	"github.com/packvium/packvium-go/intelligence/recommendations"
	"github.com/packvium/packvium-go/intelligence/simulation"
)

// Two rules are the reason the package exists rather than a spreadsheet. A packing the
// injected validator rejects is that arm's failure at any cost: its metrics never reach the
// comparison. And a recommendation may not be scored on the evidence that produced it, which
// is refused rather than warned about.

var directions = map[string]bool{"cost": false, "utilisation": true}

func pinOf(t *testing.T, version int64) simulation.ScenarioVersionPin {
	t.Helper()
	made, err := simulation.NewScenarioVersionPin(simulation.VersionPinSpec{CatalogVersion: simulation.Version(version)})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func recommendation(t *testing.T, supporting ...string) recommendations.Recommendation {
	t.Helper()
	if supporting == nil {
		supporting = []string{"trained-1"}
	}
	delta, err := recommendations.NewExpectedDelta("cost", 10, 8)
	if err != nil {
		t.Fatal(err)
	}
	made, err := recommendations.NewRecommendation(recommendations.RecommendationSpec{
		RecommendationID: "rec-1", Proposal: "cheaper cartons", SupportingOrderIDs: supporting,
		ExpectedDeltas: []recommendations.ExpectedDelta{delta}, Confidence: 1,
		Constraints: []string{"no rejected placement"}, RollbackPlan: "republish version 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

// event is one ledger entry: a shipment unless kind says otherwise.
type event struct {
	decisionID string
	at         int64
	kind       outcomes.OutcomeEventType
}

func ledgerWith(t *testing.T, events ...event) *outcomes.OutcomeLedger {
	t.Helper()
	ledger := outcomes.NewOutcomeLedger()
	for index, entry := range events {
		kind := entry.kind
		payload := map[string]interface{}{"carton_id": "box-a", "catalog_version": 1}
		if kind == "" {
			kind = outcomes.ActualCarton
		} else {
			payload = map[string]interface{}{"reason_code": "crushed_corner", "severity": "minor"}
		}
		recorded, err := outcomes.NewOutcomeEvent(outcomes.OutcomeEventSpec{EventID: "e" + string(rune('1'+index)),
			DecisionID: entry.decisionID, EventType: kind, Payload: payload, RecordedAt: entry.at})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Record(recorded); err != nil {
			t.Fatal(err)
		}
	}
	return ledger
}

// arms is one decision's (cost, utilisation) per arm.
type arms struct{ baseline, treatment [2]float64 }

// replayOf is an evaluator over a table and a validator rejecting the (decision, arm) pairs
// in invalid.
func replayOf(t *testing.T, table map[string]arms, invalid ...[2]string) Replay {
	baselinePin := pinOf(t, 1)
	evaluator := func(decisionID string, pin simulation.ScenarioVersionPin) (OrderEvaluationArtifact, error) {
		name, values := "treatment", table[decisionID].treatment
		if pin.Equal(baselinePin) {
			name, values = "baseline", table[decisionID].baseline
		}
		run, err := simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: decisionID, Succeeded: true,
			Metrics: map[string]float64{"cost": values[0], "utilisation": values[1]}})
		if err != nil {
			return OrderEvaluationArtifact{}, err
		}
		return NewOrderEvaluationArtifact(run, map[string]interface{}{"decision_id": decisionID}, map[string]interface{}{"arm": name})
	}
	validator := func(request, result interface{}) (ValidationVerdict, error) {
		pair := [2]string{request.(map[string]interface{})["decision_id"].(string), result.(map[string]interface{})["arm"].(string)}
		for _, rejected := range invalid {
			if rejected == pair {
				return NewValidationVerdict(false, []string{"unsupported_item"})
			}
		}
		return NewValidationVerdict(true, nil)
	}
	return Replay{BaselinePin: baselinePin, TreatmentPin: pinOf(t, 2), Evaluator: evaluator, Validator: validator,
		HigherIsBetter: directions}
}

func evaluate(t *testing.T, table map[string]arms, ledger *outcomes.OutcomeLedger, decisionIDs []string, splitAt int64,
	rec recommendations.Recommendation, invalid ...[2]string) (HoldoutEvaluation, error) {
	replay := replayOf(t, table, invalid...)
	replay.DecisionIDs = decisionIDs
	return EvaluateOnHistory(rec, ledger, replay, splitAt)
}

func evaluated(t *testing.T, table map[string]arms, ledger *outcomes.OutcomeLedger, decisionIDs []string, invalid ...[2]string) HoldoutEvaluation {
	t.Helper()
	result, err := evaluate(t, table, ledger, decisionIDs, 500, recommendation(t), invalid...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

var better = arms{[2]float64{10, 0.7}, [2]float64{8, 0.8}}

// ----------------------------------------------------- the split is by recorded time

func TestDecisionsRecordedBeforeTheSplitAreTraining(t *testing.T) {
	result := evaluated(t, map[string]arms{"trained-1": better, "held-1": better},
		ledgerWith(t, event{"trained-1", 100, ""}, event{"held-1", 900, ""}), []string{"trained-1", "held-1"})
	if !reflect.DeepEqual(result.TrainingDecisionIDs(), []string{"trained-1"}) || !reflect.DeepEqual(result.HoldoutDecisionIDs(), []string{"held-1"}) {
		t.Fatalf("%v %v", result.TrainingDecisionIDs(), result.HoldoutDecisionIDs())
	}
}

func TestOnlyHeldOutDecisionsAreScored(t *testing.T) {
	result := evaluated(t, map[string]arms{"trained-1": better, "held-1": better},
		ledgerWith(t, event{"trained-1", 100, ""}, event{"held-1", 900, ""}), []string{"trained-1", "held-1"})
	if decisions := result.Decisions(); len(decisions) != 1 || decisions[0].DecisionID() != "held-1" {
		t.Fatalf("%v", decisions)
	}
}

// A later correction must not drag a decision across the split it was recorded on the other
// side of.
func TestTheSplitUsesTheEarliestEventForADecision(t *testing.T) {
	ledger := ledgerWith(t, event{"held-1", 100, ""}, event{"held-1", 900, outcomes.Damage})
	result, err := evaluate(t, map[string]arms{"held-1": better}, ledger, []string{"held-1"}, 500, recommendation(t, "other"))
	if err != nil || !reflect.DeepEqual(result.TrainingDecisionIDs(), []string{"held-1"}) || len(result.Decisions()) != 0 {
		t.Fatalf("%v %v", result, err)
	}
}

// It has no timestamp, so it cannot be placed on either side; defaulting it would be guessing.
func TestADecisionWithNoRecordedEventsIsRefused(t *testing.T) {
	_, err := evaluate(t, nil, outcomes.NewOutcomeLedger(), []string{"never-shipped"}, 500, recommendation(t))
	if err == nil || err.Error() != "decision 'never-shipped' has no recorded events, so it cannot be split by time" {
		t.Fatalf("got %v", err)
	}
}

func TestANegativeSplitIsRefused(t *testing.T) {
	_, err := evaluate(t, nil, ledgerWith(t, event{"held-1", 900, ""}), []string{"held-1"}, -1, recommendation(t))
	if err == nil || err.Error() != "split_at cannot be negative" {
		t.Fatalf("got %v", err)
	}
}

// An empty holdout would report zero regressions and read as a clean bill.
func TestAnEmptyDecisionListIsRefused(t *testing.T) {
	_, err := evaluate(t, nil, outcomes.NewOutcomeLedger(), nil, 500, recommendation(t))
	if err == nil || err.Error() != "at least one decision id is required" {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------- a proposal cannot be scored on its own evidence

func TestACitedDecisionInTheHoldoutIsRefusedByName(t *testing.T) {
	_, err := evaluate(t, map[string]arms{"trained-1": better}, ledgerWith(t, event{"trained-1", 900, ""}),
		[]string{"trained-1"}, 500, recommendation(t))
	var refusal *SupportingEvidenceInHoldoutError
	if !errors.As(err, &refusal) || err.Error() !=
		"recommendation 'rec-1' cites held-out decision(s) ['trained-1']; it may not be scored on its own evidence" {
		t.Fatalf("got %v", err)
	}
}

func TestTheSameDecisionOnTheTrainingSideIsFine(t *testing.T) {
	result := evaluated(t, map[string]arms{"trained-1": better}, ledgerWith(t, event{"trained-1", 100, ""}), []string{"trained-1"})
	if !reflect.DeepEqual(result.TrainingDecisionIDs(), []string{"trained-1"}) {
		t.Fatalf("%v", result.TrainingDecisionIDs())
	}
}

func TestTheRefusalIsAHoldoutError(t *testing.T) {
	var base *HoldoutError
	if !errors.As(&SupportingEvidenceInHoldoutError{"x"}, &base) {
		t.Fatal("not a HoldoutError")
	}
}

// -------------------------------------------------------- the verdict per decision

func verdict(t *testing.T, baseline, treatment [2]float64, invalid ...[2]string) DecisionOutcome {
	t.Helper()
	result := evaluated(t, map[string]arms{"held-1": {baseline, treatment}}, ledgerWith(t, event{"held-1", 900, ""}),
		[]string{"held-1"}, invalid...)
	return result.Decisions()[0]
}

func TestBetterOnEveryAxisIsAnImprovement(t *testing.T) {
	if got := verdict(t, [2]float64{10, 0.7}, [2]float64{8, 0.9}).Verdict(); got != Improved {
		t.Fatal(got)
	}
}

func TestWorseOnEveryAxisIsARegression(t *testing.T) {
	if got := verdict(t, [2]float64{8, 0.9}, [2]float64{10, 0.7}).Verdict(); got != Regressed {
		t.Fatal(got)
	}
}

func TestCheaperButSparserIsATradeOffRatherThanAWin(t *testing.T) {
	if got := verdict(t, [2]float64{10, 0.9}, [2]float64{8, 0.7}).Verdict(); got != TradedOff {
		t.Fatal(got)
	}
}

// Neither dominates, so neither is named: the report saying "no difference".
func TestIdenticalMetricsAreATradeOffRatherThanAWin(t *testing.T) {
	if got := verdict(t, [2]float64{10, 0.8}, [2]float64{10, 0.8}).Verdict(); got != TradedOff {
		t.Fatal(got)
	}
}

// The rule the package exists to enforce: the treatment is cheaper and denser, and the
// validator refused the placement, so its metrics are discarded rather than discounted.
func TestARejectedTreatmentIsARegressionHoweverCheapItWas(t *testing.T) {
	outcome := verdict(t, [2]float64{16, 0.6}, [2]float64{10, 0.9}, [2]string{"held-1", "treatment"})
	if outcome.Verdict() != Regressed || outcome.TreatmentValid() || !reflect.DeepEqual(outcome.TreatmentCodes(), []string{"unsupported_item"}) {
		t.Fatalf("%+v", outcome)
	}
}

func TestARejectedBaselineLeavesAValidTreatmentAnImprovement(t *testing.T) {
	outcome := verdict(t, [2]float64{8, 0.9}, [2]float64{10, 0.7}, [2]string{"held-1", "baseline"})
	if outcome.Verdict() != Improved || outcome.BaselineValid() {
		t.Fatalf("%+v", outcome)
	}
}

func TestBothArmsRejectedIsUnpackableRatherThanATie(t *testing.T) {
	outcome := verdict(t, [2]float64{10, 0.7}, [2]float64{8, 0.9}, [2]string{"held-1", "baseline"}, [2]string{"held-1", "treatment"})
	if outcome.Verdict() != Unpackable {
		t.Fatalf("%+v", outcome)
	}
}

// -------------------------------------- realised risk is reported against the arm that shipped

// Damage happened under the carton that actually shipped; crediting the treatment with
// avoiding it would score a counterfactual nobody ran.
func TestRecordedDamageIsAttachedToTheBaseline(t *testing.T) {
	ledger := ledgerWith(t, event{"held-1", 900, ""}, event{"held-1", 950, outcomes.Damage})
	result := evaluated(t, map[string]arms{"held-1": {[2]float64{10, 0.7}, [2]float64{8, 0.9}}}, ledger, []string{"held-1"})
	if risk := result.Decisions()[0].BaselineRealisedRisk(); !reflect.DeepEqual(risk, []string{"damage"}) {
		t.Fatalf("%v", risk)
	}
}

func TestACleanHistoryCarriesNoRealisedRisk(t *testing.T) {
	result := evaluated(t, map[string]arms{"held-1": {[2]float64{10, 0.7}, [2]float64{8, 0.9}}},
		ledgerWith(t, event{"held-1", 900, ""}), []string{"held-1"})
	if risk := result.Decisions()[0].BaselineRealisedRisk(); len(risk) != 0 {
		t.Fatalf("%v", risk)
	}
}

// ------------------------------------------------------------------- the tally

func TestEachVerdictIsCountedOnce(t *testing.T) {
	table := map[string]arms{
		"up":       {[2]float64{10, 0.7}, [2]float64{8, 0.9}},
		"down":     {[2]float64{8, 0.9}, [2]float64{10, 0.7}},
		"sideways": {[2]float64{10, 0.9}, [2]float64{8, 0.7}},
	}
	ledger := ledgerWith(t, event{"up", 900, ""}, event{"down", 910, ""}, event{"sideways", 920, ""})
	result := evaluated(t, table, ledger, []string{"up", "down", "sideways"})
	if result.Improved() != 1 || result.Regressed() != 1 || result.TradedOff() != 1 || result.Unpackable() != 0 {
		t.Fatalf("%d %d %d %d", result.Improved(), result.Regressed(), result.TradedOff(), result.Unpackable())
	}
}

func TestTheEvaluationRecordsBothPinsItCompared(t *testing.T) {
	result := evaluated(t, map[string]arms{"held-1": better}, ledgerWith(t, event{"held-1", 900, ""}), []string{"held-1"})
	if !result.BaselinePin().Equal(pinOf(t, 1)) || !result.TreatmentPin().Equal(pinOf(t, 2)) || result.SplitAt() != 500 {
		t.Fatalf("%+v", result)
	}
}

// ----------------------------------------- the artifact carries what the validator reads

func runOf(t *testing.T) simulation.OrderRunResult {
	made, err := simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: "d1", Succeeded: true, Metrics: map[string]float64{"cost": 1}})
	if err != nil {
		t.Fatal(err)
	}
	return made
}

func TestACompleteArtifactIsAccepted(t *testing.T) {
	artifact, err := NewOrderEvaluationArtifact(runOf(t), map[string]interface{}{"a": 1}, map[string]interface{}{"b": 2})
	if err != nil || artifact.Run().OrderID() != "d1" {
		t.Fatalf("%v %v", artifact, err)
	}
}

// The validator is handed the request/result pair precisely so that no score can influence it.
func TestAnArtifactWithoutThePairIsRefused(t *testing.T) {
	for _, pair := range [][2]interface{}{{nil, map[string]interface{}{"b": 2}}, {map[string]interface{}{"a": 1}, nil}} {
		_, err := NewOrderEvaluationArtifact(runOf(t), pair[0], pair[1])
		if err == nil || !strings.Contains(err.Error(), "request/result pair") {
			t.Fatalf("got %v", err)
		}
	}
}

// ------------------------------------------- the record types guard their own claims

func TestAVerdictAndAnOutcomeGuardTheirClaims(t *testing.T) {
	if _, err := NewValidationVerdict(true, []string{"x"}); err == nil || err.Error() != "a valid verdict cannot carry rejection codes" {
		t.Errorf("got %v", err)
	}
	if _, err := NewValidationVerdict(false, nil); err == nil || err.Error() != "a rejection must say which rules it failed" {
		t.Errorf("got %v", err)
	}
	for _, c := range []struct {
		spec    DecisionOutcomeSpec
		message string
	}{
		{DecisionOutcomeSpec{Verdict: Improved, TreatmentValid: true}, "decision_id is required"},
		{DecisionOutcomeSpec{DecisionID: "d", Verdict: "better"}, "unknown verdict 'better'"},
		{DecisionOutcomeSpec{DecisionID: "d", Verdict: Improved}, "a packing the validator rejected can never be recorded as an improvement"},
	} {
		if _, err := NewDecisionOutcome(c.spec); err == nil || err.Error() != c.message {
			t.Errorf("got %v, want %q", err, c.message)
		}
	}
}
