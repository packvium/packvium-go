// Prove a carton change is better before you publish it.
//
// Run it from packvium-go/:
//
//	go run ./examples/intelligence
//
// Every other example asks the engine to pack one shipment. This one asks a different
// question, the one that comes up when you already pack well and want to change something:
// is the new carton set actually an improvement, and how would I know?
//
// Four functions answer it, and none of them returns a single blended number. A score that
// folds cost and utilisation together can name a "winner" that is worse on the axis you
// actually care about, so what you get back is a per-order Pareto report: which orders
// improved, which regressed, and which genuinely traded one axis for another.
//
// Nothing here reads the clock or the network. The scenarios are pinned to catalog versions
// you supply, so the same comparison replays to the same answer next year.
package main

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/packvium/packvium-go/intelligence/holdout"
	"github.com/packvium/packvium-go/intelligence/outcomes"
	"github.com/packvium/packvium-go/intelligence/recommendations"
	"github.com/packvium/packvium-go/intelligence/simulation"
)

// measured is one order's (cost_minor, utilisation) under one carton set; nil means that set
// produced no answer for the order.
type measured *[2]float64

func m(cost, utilisation float64) measured { return &[2]float64{cost, utilisation} }

// Two carton sets over the same five orders. You would measure these by packing each order
// twice; they are written out here so the example has nothing to hide.
//
// cost_minor is in cents and lower is better. utilisation is filled volume over container
// volume and higher is better. Order 5 is the interesting one: the proposed cartons cannot
// pack it at all.
var (
	orderIDs = []string{"order-1", "order-2", "order-3", "order-4", "order-5"}
	measures = map[string]map[string]measured{
		"order-1": {"baseline": m(1240, 0.71), "treatment": m(1120, 0.76)},
		"order-2": {"baseline": m(980, 0.68), "treatment": m(910, 0.72)},
		"order-3": {"baseline": m(1500, 0.80), "treatment": m(1600, 0.83)},
		"order-4": {"baseline": m(2100, 0.64), "treatment": m(1890, 0.69)},
		"order-5": {"baseline": m(1350, 0.70), "treatment": nil},
	}
)

// Both directions are stated explicitly. There is no default, because guessing that runtime
// or cost is "higher is better" would silently invert the whole report.
var higherIsBetter = map[string]bool{"cost_minor": false, "utilisation": true}

var rule = strings.Repeat("=", 78)

func main() {
	// A pin is what makes a comparison replayable: it names the catalog version each arm was
	// run against rather than whatever is current when you happen to run it.
	baselinePin := pin(1)
	treatmentPin := pin(2)

	baseline, treatment := compare(baselinePin, treatmentPin)
	recommendation := propose(baseline, treatment)
	ledger := history()
	replay(recommendation, ledger, baselinePin, treatmentPin)
	ownEvidence(recommendation, ledger, baselinePin, treatmentPin)
}

func pin(catalogVersion int64) simulation.ScenarioVersionPin {
	made, err := simulation.NewScenarioVersionPin(simulation.VersionPinSpec{CatalogVersion: simulation.Version(catalogVersion)})
	check(err)
	return made
}

// arm is an evaluator for one carton set. In your code this calls serialization.Pack; here it
// replays the measurements above so the example teaches the comparison and not the packing.
func arm(name string) simulation.OrderEvaluator {
	return func(orderID string, _ simulation.ScenarioVersionPin) (simulation.OrderRunResult, error) {
		values := measures[orderID][name]
		if values == nil {
			return simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: orderID,
				FailureReason: "no carton in the proposed set fits this order"})
		}
		return simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: orderID, Succeeded: true,
			Metrics: map[string]float64{"cost_minor": values[0], "utilisation": values[1]}})
	}
}

func heading(title string) {
	fmt.Println(rule)
	fmt.Println(title)
	fmt.Println(rule)
	fmt.Println()
}

func paragraph(lines ...string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

func compare(baselinePin, treatmentPin simulation.ScenarioVersionPin) (simulation.ScenarioResult, simulation.ScenarioResult) {
	heading("1. Two scenarios, compared order by order")
	baseline, err := simulation.RunScenario("current-cartons", baselinePin, orderIDs, arm("baseline"))
	check(err)
	treatment, err := simulation.RunScenario("proposed-cartons", treatmentPin, orderIDs, arm("treatment"))
	check(err)
	reports, err := simulation.CompareScenarios(baseline, treatment, higherIsBetter)
	check(err)
	for _, report := range reports {
		if report.Winner() != "" {
			fmt.Printf("  %s: %s\n", report.Profile(), report.Winner())
		} else {
			fmt.Printf("  %s: no winner — %s are both Pareto-optimal\n", report.Profile(), strings.Join(report.ParetoOptimal(), " and "))
		}
	}
	fmt.Println()
	paragraph(
		"  order-3 has no winner because the trade-off is real: the proposed cartons cost",
		"  more and pack denser. A blended score would have picked one and hidden that.",
		"",
		"  order-5 names `baseline` as winner for a duller reason — the proposed cartons",
		"  produced no answer there, so there was only one candidate to be optimal. That",
		"  is not a baseline win, and the next step is careful not to count it as one.",
		"")
	return baseline, treatment
}

func evidence(minimumConfidence float64) recommendations.Evidence {
	return recommendations.Evidence{
		Constraints:  []string{"no placement the validator would reject"},
		RollbackPlan: "republish catalog version 1", MinimumCohortSize: 4, MinimumConfidence: minimumConfidence,
	}
}

func propose(baseline, treatment simulation.ScenarioResult) recommendations.Recommendation {
	heading("2. A proposal, or an explicit refusal to make one")
	// The paired cohort is four orders out of five, so confidence is 0.8. Ask for more than
	// the evidence supports and you get nil rather than a proposal with a caveat attached.
	strict, err := recommendations.Propose("rec-cartons-2024", "adopt the proposed carton set", baseline, treatment, evidence(0.9))
	check(err)
	if strict != nil {
		log.Fatal("the evidence now clears a bar it should not")
	}
	fmt.Println("  minimum_confidence=0.9 -> nil")
	paragraph(
		"  Four of five orders are comparable, so confidence is 0.80 and the bar is not",
		"  met. `nil` is the whole answer: there is no low-confidence proposal to weigh.",
		"")

	recommendation, err := recommendations.Propose("rec-cartons-2024", "adopt the proposed carton set", baseline, treatment, evidence(0.75))
	check(err)
	if recommendation == nil {
		log.Fatal("the evidence no longer supports a proposal")
	}
	fmt.Printf("  minimum_confidence=0.75 -> %s\n", recommendation.RecommendationID())
	fmt.Printf("  supported by: %s\n", strings.Join(recommendation.SupportingOrderIDs(), ", "))
	fmt.Printf("  confidence:   %.2f\n", recommendation.Confidence())
	for _, delta := range recommendation.ExpectedDeltas() {
		fmt.Printf("    %-12s %8.3f -> %8.3f\n", delta.Metric(), delta.BaselineMean(), delta.TreatmentMean())
	}
	fmt.Println()
	paragraph(
		"  order-5 is absent from the supporting orders. A delta is a paired comparison,",
		"  so an order only one arm could pack contributes to neither mean.",
		"")
	return *recommendation
}

// history is an append-only ledger of what actually happened to shipments. Its timestamps
// are what a holdout split is made of.
//
// Each event type accepts a closed set of fields and nothing else -- damage takes a reason
// and a severity, not a carton id. A ledger that accepted arbitrary payloads would be a log;
// the point of this one is that a replay can rely on what it finds there.
func history() *outcomes.OutcomeLedger {
	shipped := func(carton string) map[string]interface{} {
		return map[string]interface{}{"carton_id": carton, "catalog_version": 1}
	}
	events := []outcomes.OutcomeEventSpec{
		{DecisionID: "order-1", EventType: outcomes.ActualCarton, RecordedAt: 100, Payload: shipped("box-a")},
		{DecisionID: "order-2", EventType: outcomes.ActualCarton, RecordedAt: 120, Payload: shipped("box-a")},
		{DecisionID: "shipment-101", EventType: outcomes.ActualCarton, RecordedAt: 900, Payload: shipped("box-b")},
		{DecisionID: "shipment-102", EventType: outcomes.ActualCarton, RecordedAt: 910, Payload: shipped("box-a")},
		{DecisionID: "shipment-103", EventType: outcomes.ActualCarton, RecordedAt: 920, Payload: shipped("box-c")},
		{DecisionID: "shipment-104", EventType: outcomes.ActualCarton, RecordedAt: 930, Payload: shipped("box-b")},
		// A real bad outcome under the carton that actually shipped. It is reported against
		// the baseline and never credited to the treatment, because it happened.
		{DecisionID: "shipment-104", EventType: outcomes.Damage, RecordedAt: 940,
			Payload: map[string]interface{}{"reason_code": "crushed_corner", "severity": "minor"}},
	}
	ledger := outcomes.NewOutcomeLedger()
	for index, spec := range events {
		spec.EventID = fmt.Sprintf("e%d", index+1)
		event, err := outcomes.NewOutcomeEvent(spec)
		check(err)
		_, err = ledger.Record(event)
		check(err)
	}
	return ledger
}

// replayed is what re-packing each historical decision produced under each carton set.
var replayed = map[string]map[string][2]float64{
	"shipment-101": {"baseline": {1400, 0.66}, "treatment": {1210, 0.73}},
	"shipment-102": {"baseline": {1150, 0.74}, "treatment": {1180, 0.77}},
	// Cheaper under the proposed cartons -- and unstackable, which the validator catches.
	"shipment-103": {"baseline": {1600, 0.62}, "treatment": {1050, 0.81}},
	"shipment-104": {"baseline": {990, 0.69}, "treatment": {940, 0.71}},
}

var invalid = map[[2]string][]string{{"shipment-103", "treatment"}: {"unsupported_item"}}

// historyReplay re-packs one historical decision under whichever carton set the pin names,
// and checks it with your own independent validator, run over the packing rather than over
// its score.
func historyReplay(baselinePin, treatmentPin simulation.ScenarioVersionPin, decisionIDs ...string) holdout.Replay {
	evaluator := func(decisionID string, pin simulation.ScenarioVersionPin) (holdout.OrderEvaluationArtifact, error) {
		name := "treatment"
		if pin.Equal(baselinePin) {
			name = "baseline"
		}
		values := replayed[decisionID][name]
		run, err := simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: decisionID, Succeeded: true,
			Metrics: map[string]float64{"cost_minor": values[0], "utilisation": values[1]}})
		if err != nil {
			return holdout.OrderEvaluationArtifact{}, err
		}
		// The request/result pair is what the validator is handed. It deliberately carries no
		// score, so nothing it decides can be influenced by how good the packing looked.
		return holdout.NewOrderEvaluationArtifact(run, map[string]interface{}{"decision_id": decisionID},
			map[string]interface{}{"arm": name})
	}
	validator := func(request, result interface{}) (holdout.ValidationVerdict, error) {
		key := [2]string{request.(map[string]interface{})["decision_id"].(string), result.(map[string]interface{})["arm"].(string)}
		codes := invalid[key]
		return holdout.NewValidationVerdict(len(codes) == 0, codes)
	}
	return holdout.Replay{DecisionIDs: decisionIDs, BaselinePin: baselinePin, TreatmentPin: treatmentPin,
		Evaluator: evaluator, Validator: validator, HigherIsBetter: higherIsBetter}
}

func replay(recommendation recommendations.Recommendation, ledger *outcomes.OutcomeLedger, baselinePin, treatmentPin simulation.ScenarioVersionPin) {
	heading("3. Replay against history the proposal has never seen")
	evaluation, err := holdout.EvaluateOnHistory(recommendation, ledger,
		historyReplay(baselinePin, treatmentPin, "order-1", "order-2", "shipment-101", "shipment-102", "shipment-103", "shipment-104"), 500)
	check(err)
	paragraph(
		"  split_at=500 — required, with no default, because how much history is enough",
		"  is a claim about your business that this library cannot make for you.",
		"")
	fmt.Printf("  training: %s\n", strings.Join(evaluation.TrainingDecisionIDs(), ", "))
	fmt.Printf("  held out: %s\n", strings.Join(evaluation.HoldoutDecisionIDs(), ", "))
	fmt.Println()
	for _, decision := range evaluation.Decisions() {
		note := ""
		if codes := decision.TreatmentCodes(); len(codes) > 0 {
			note = fmt.Sprintf("  (treatment rejected: %s)", strings.Join(codes, ", "))
		} else if risk := decision.BaselineRealisedRisk(); len(risk) > 0 {
			note = fmt.Sprintf("  (baseline actually suffered: %s)", strings.Join(risk, ", "))
		}
		fmt.Printf("    %-14s %s%s\n", decision.DecisionID(), decision.Verdict(), note)
	}
	fmt.Println()
	fmt.Printf("  improved %d, regressed %d, traded off %d, unpackable %d\n",
		evaluation.Improved(), evaluation.Regressed(), evaluation.TradedOff(), evaluation.Unpackable())
	fmt.Println()
	paragraph(
		"  shipment-104's note is negative evidence about the baseline, not a reason the",
		"  treatment won. Damage, returns, repacks and operator overrides happened under",
		"  the carton that actually shipped, so they are reported against that arm and",
		"  never credited to the one that was never tried.",
		"",
		"  shipment-103 is the one to read twice. The proposed cartons packed it for 1050",
		"  against 1600 and denser besides — and the independent validator rejected the",
		"  placement, so it counts as a regression. There is no cost at which a packing",
		"  your validator refuses becomes an improvement; the metrics are discarded, not",
		"  discounted.",
		"")
}

func ownEvidence(recommendation recommendations.Recommendation, ledger *outcomes.OutcomeLedger, baselinePin, treatmentPin simulation.ScenarioVersionPin) {
	heading("4. A proposal cannot be scored on its own evidence")
	// Move the split earlier and order-1 -- which the recommendation cites -- lands in the
	// held-out set. That is marking your own homework, so it is refused rather than reported.
	_, err := holdout.EvaluateOnHistory(recommendation, ledger, historyReplay(baselinePin, treatmentPin, "order-1", "shipment-101"), 50)
	var refusal *holdout.SupportingEvidenceInHoldoutError
	if !errors.As(err, &refusal) {
		log.Fatalf("expected a refusal, got %v", err)
	}
	fmt.Printf("  refused: %s\n", refusal)
	fmt.Println()
	paragraph(
		"  The refusal is the feature. A holdout score is worth exactly as much as the",
		"  separation between the evidence that produced the proposal and the evidence",
		"  that tests it, and nothing else in the call can enforce that separation.")
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
