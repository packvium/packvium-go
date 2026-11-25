// Package holdout replays a recommendation against history it was not derived from: would
// it have improved past decisions, scored on a slice it never saw?
//
// Three rules shape the package, and they are the difference between a backtest and a story
// told about one.
//
// The split is by time, and the training view is as of that time. A decision is held out
// when its earliest recorded event is at or after the split. The training side sees only
// events recorded strictly before it (outcomes.OutcomeLedger.ViewAsOf), so a later
// correction stays in the ledger but does not reach backwards into what was knowable. A
// recommendation whose supporting orders reach into the holdout is refused rather than
// scored: it would be citing its own answer sheet.
//
// Most of the ledger cannot be scored counterfactually. It records what happened under the
// carton that actually shipped; re-running the treatment produces a different carton, so only
// the deterministically recomputable part -- the packing and what the pinned tariff prices
// from it -- can be compared. Damage, returns, repacks and operator overrides were realised
// under a box the treatment did not choose. They are carried as negative evidence about the
// baseline and never as a predicted benefit of the treatment.
//
// The validator runs unconditionally, before any metric is compared, and its verdict is not
// an input to a score. An arm the validator rejects has failed for that decision, whatever it
// cost, and the validator is handed no score, so nothing learned from history can reach it.
//
// The aggregate is a count of decisions, never a mean delta: a mean over a cohort is one
// number a few large orders can carry by themselves.
package holdout

import (
	"fmt"
	"sort"
	"strings"

	"github.com/packvium/packvium-go/intelligence/outcomes"
	"github.com/packvium/packvium-go/intelligence/recommendations"
	"github.com/packvium/packvium-go/intelligence/simulation"
	"github.com/packvium/packvium-go/internal/pyrepr"
)

// The four verdicts a held-out decision can get.
const (
	Improved   = "improved"
	Regressed  = "regressed"
	TradedOff  = "traded_off"
	Unpackable = "unpackable"
)

// notCounterfactual are the ledger facts describing something a person or the world did to the
// carton that actually shipped. Under a different carton they may not have happened at all, so
// they are reported against the baseline and never credited to the treatment.
var notCounterfactual = map[outcomes.OutcomeEventType]bool{
	outcomes.Damage: true, outcomes.Return: true, outcomes.Repack: true, outcomes.OperatorOverride: true,
}

// HoldoutError is the base of every holdout error this package returns that is not a plain
// argument error. errors.As matches SupportingEvidenceInHoldoutError as one too.
type HoldoutError struct{ message string }

// Error is the message, in the reference's wording.
func (e *HoldoutError) Error() string { return e.message }

// SupportingEvidenceInHoldoutError is a recommendation citing a decision the split holds out.
// A recommendation evaluated on the evidence it was derived from measures how well it
// remembers, not whether it generalises.
type SupportingEvidenceInHoldoutError struct{ message string }

// Error is the message, in the reference's wording.
func (e *SupportingEvidenceInHoldoutError) Error() string { return e.message }

// Unwrap makes the error match *HoldoutError, its base in the reference.
func (e *SupportingEvidenceInHoldoutError) Unwrap() error { return &HoldoutError{e.message} }

// ValidationVerdict is an independent validator's answer about one packing. It carries no
// score and no metric: it is produced by a callback that is never handed one.
type ValidationVerdict struct {
	valid bool
	codes []string
}

// NewValidationVerdict refuses a valid verdict with rejection codes and a rejection without.
func NewValidationVerdict(valid bool, codes []string) (ValidationVerdict, error) {
	if valid && len(codes) > 0 {
		return ValidationVerdict{}, fmt.Errorf("a valid verdict cannot carry rejection codes")
	}
	if !valid && len(codes) == 0 {
		return ValidationVerdict{}, fmt.Errorf("a rejection must say which rules it failed")
	}
	return ValidationVerdict{valid, append([]string(nil), codes...)}, nil
}

// Valid is whether the packing passed.
func (v ValidationVerdict) Valid() bool { return v.valid }

// Codes are the rules a rejected packing failed.
func (v ValidationVerdict) Codes() []string { return append([]string(nil), v.codes...) }

// OrderEvaluationArtifact is one arm's run of one decision, plus the request/result pair an
// independent check needs. A run summary alone cannot be independently validated, so the
// pair is required: the validator is given the packing, not a summary of it.
type OrderEvaluationArtifact struct {
	run             simulation.OrderRunResult
	request, result interface{}
}

// NewOrderEvaluationArtifact refuses an artifact without its request or its result.
func NewOrderEvaluationArtifact(run simulation.OrderRunResult, request, result interface{}) (OrderEvaluationArtifact, error) {
	if request == nil || result == nil {
		return OrderEvaluationArtifact{}, fmt.Errorf("a holdout artifact must carry the request/result pair the validator reads; " +
			"a run summary alone cannot be independently validated")
	}
	return OrderEvaluationArtifact{run, request, result}, nil
}

// Run is the arm's outcome.
func (a OrderEvaluationArtifact) Run() simulation.OrderRunResult { return a.run }

// Request is what the validator reads, unchanged.
func (a OrderEvaluationArtifact) Request() interface{} { return a.request }

// Result is what the validator reads, unchanged.
func (a OrderEvaluationArtifact) Result() interface{} { return a.result }

// Evaluator re-runs one historical decision under one pin.
type Evaluator func(decisionID string, pin simulation.ScenarioVersionPin) (OrderEvaluationArtifact, error)

// IndependentValidator judges one packing from its request and result. It is separate from
// the evaluator on purpose: an engine that graded its own homework would make the gate a
// comment.
type IndependentValidator func(request, result interface{}) (ValidationVerdict, error)

// DecisionOutcomeSpec is what the replay found for one held-out decision.
type DecisionOutcomeSpec struct {
	DecisionID           string
	Verdict              string
	BaselineValid        bool
	TreatmentValid       bool
	BaselineCodes        []string
	TreatmentCodes       []string
	BaselineRealisedRisk []string
}

// DecisionOutcome is what the replay found for one held-out decision, retained rather than
// summarised: every verdict keeps the validator codes that produced it and the realised-risk
// events the baseline actually incurred.
type DecisionOutcome struct{ spec DecisionOutcomeSpec }

// NewDecisionOutcome refuses an unknown verdict, and an improvement the validator rejected.
func NewDecisionOutcome(spec DecisionOutcomeSpec) (DecisionOutcome, error) {
	switch {
	case spec.DecisionID == "":
		return DecisionOutcome{}, fmt.Errorf("decision_id is required")
	case !knownVerdict(spec.Verdict):
		return DecisionOutcome{}, fmt.Errorf("unknown verdict %s", pyrepr.String(spec.Verdict))
	case spec.Verdict == Improved && !spec.TreatmentValid:
		return DecisionOutcome{}, fmt.Errorf("a packing the validator rejected can never be recorded as an improvement")
	}
	frozen := spec
	frozen.BaselineCodes = append([]string(nil), spec.BaselineCodes...)
	frozen.TreatmentCodes = append([]string(nil), spec.TreatmentCodes...)
	frozen.BaselineRealisedRisk = append([]string(nil), spec.BaselineRealisedRisk...)
	return DecisionOutcome{frozen}, nil
}

func knownVerdict(verdict string) bool {
	switch verdict {
	case Improved, Regressed, TradedOff, Unpackable:
		return true
	}
	return false
}

// DecisionID is the held-out decision.
func (d DecisionOutcome) DecisionID() string { return d.spec.DecisionID }

// Verdict is Improved, Regressed, TradedOff or Unpackable.
func (d DecisionOutcome) Verdict() string { return d.spec.Verdict }

// BaselineValid is whether the validator passed the baseline's packing.
func (d DecisionOutcome) BaselineValid() bool { return d.spec.BaselineValid }

// TreatmentValid is whether the validator passed the treatment's packing.
func (d DecisionOutcome) TreatmentValid() bool { return d.spec.TreatmentValid }

// BaselineCodes are the rules the baseline's packing failed.
func (d DecisionOutcome) BaselineCodes() []string {
	return append([]string(nil), d.spec.BaselineCodes...)
}

// TreatmentCodes are the rules the treatment's packing failed.
func (d DecisionOutcome) TreatmentCodes() []string {
	return append([]string(nil), d.spec.TreatmentCodes...)
}

// BaselineRealisedRisk are the bad outcomes the shipped carton actually incurred, sorted:
// negative evidence about the baseline only.
func (d DecisionOutcome) BaselineRealisedRisk() []string {
	return append([]string(nil), d.spec.BaselineRealisedRisk...)
}

// HoldoutEvaluation is the complete record of one holdout evaluation: the split, both pins
// and every per-decision verdict, not just the counts, so the run can be argued with rather
// than merely believed.
type HoldoutEvaluation struct {
	recommendationID          string
	splitAt                   int64
	baselinePin, treatmentPin simulation.ScenarioVersionPin
	trainingIDs, holdoutIDs   []string
	decisions                 []DecisionOutcome
}

// RecommendationID is the recommendation evaluated.
func (h HoldoutEvaluation) RecommendationID() string { return h.recommendationID }

// SplitAt is the instant that divides training from holdout.
func (h HoldoutEvaluation) SplitAt() int64 { return h.splitAt }

// BaselinePin is the pin the baseline arm ran against.
func (h HoldoutEvaluation) BaselinePin() simulation.ScenarioVersionPin { return h.baselinePin }

// TreatmentPin is the pin the treatment arm ran against.
func (h HoldoutEvaluation) TreatmentPin() simulation.ScenarioVersionPin { return h.treatmentPin }

// TrainingDecisionIDs are the decisions recorded before the split, in the order given.
func (h HoldoutEvaluation) TrainingDecisionIDs() []string {
	return append([]string(nil), h.trainingIDs...)
}

// HoldoutDecisionIDs are the decisions recorded at or after the split, in the order given.
func (h HoldoutEvaluation) HoldoutDecisionIDs() []string {
	return append([]string(nil), h.holdoutIDs...)
}

// Decisions is the verdict for every held-out decision, in holdout order.
func (h HoldoutEvaluation) Decisions() []DecisionOutcome {
	return append([]DecisionOutcome(nil), h.decisions...)
}

func (h HoldoutEvaluation) count(verdict string) int {
	total := 0
	for _, decision := range h.decisions {
		if decision.spec.Verdict == verdict {
			total++
		}
	}
	return total
}

// Improved counts the decisions the treatment improved.
func (h HoldoutEvaluation) Improved() int { return h.count(Improved) }

// Regressed counts the decisions the treatment made worse.
func (h HoldoutEvaluation) Regressed() int { return h.count(Regressed) }

// TradedOff counts the decisions with a genuine trade-off or no difference.
func (h HoldoutEvaluation) TradedOff() int { return h.count(TradedOff) }

// Unpackable counts the decisions neither arm packed validly.
func (h HoldoutEvaluation) Unpackable() int { return h.count(Unpackable) }

// Replay is what a holdout evaluation compares: the decisions, the two pinned arms, how to
// re-run and independently validate one decision, and the direction of every compared axis.
type Replay struct {
	DecisionIDs    []string
	BaselinePin    simulation.ScenarioVersionPin
	TreatmentPin   simulation.ScenarioVersionPin
	Evaluator      Evaluator
	Validator      IndependentValidator
	HigherIsBetter map[string]bool
}

// EvaluateOnHistory scores recommendation against the decisions splitAt holds out.
//
// splitAt is a separate argument, with no default: any default would be this library making a
// claim about how much history is enough for a business it cannot see. A recommendation
// citing a held-out decision is a *SupportingEvidenceInHoldoutError; a decision with no
// recorded events cannot be placed on either side of a split by time and is an error too.
func EvaluateOnHistory(recommendation recommendations.Recommendation, ledger *outcomes.OutcomeLedger, replay Replay,
	splitAt int64) (HoldoutEvaluation, error) {
	if splitAt < 0 {
		return HoldoutEvaluation{}, fmt.Errorf("split_at cannot be negative")
	}
	if len(replay.DecisionIDs) == 0 {
		return HoldoutEvaluation{}, fmt.Errorf("at least one decision id is required")
	}
	var training, holdout []string
	for _, decisionID := range replay.DecisionIDs {
		earliest, recorded := earliestEventTime(ledger, decisionID)
		if !recorded {
			return HoldoutEvaluation{}, fmt.Errorf("decision %s has no recorded events, so it cannot be split by time",
				pyrepr.String(decisionID))
		}
		if earliest >= splitAt {
			holdout = append(holdout, decisionID)
		} else {
			training = append(training, decisionID)
		}
	}
	if cited := citedIn(recommendation.SupportingOrderIDs(), holdout); len(cited) > 0 {
		return HoldoutEvaluation{}, &SupportingEvidenceInHoldoutError{fmt.Sprintf(
			"recommendation %s cites held-out decision(s) %s; it may not be scored on its own evidence",
			pyrepr.String(recommendation.RecommendationID()), listRepr(cited))}
	}
	decisions := make([]DecisionOutcome, 0, len(holdout))
	for _, decisionID := range holdout {
		decision, err := evaluateDecision(decisionID, ledger, replay)
		if err != nil {
			return HoldoutEvaluation{}, err
		}
		decisions = append(decisions, decision)
	}
	return HoldoutEvaluation{recommendation.RecommendationID(), splitAt, replay.BaselinePin, replay.TreatmentPin,
		training, holdout, decisions}, nil
}

func earliestEventTime(ledger *outcomes.OutcomeLedger, decisionID string) (int64, bool) {
	events := ledger.EventsForDecision(decisionID)
	if len(events) == 0 {
		return 0, false
	}
	earliest := events[0].RecordedAt()
	for _, event := range events[1:] {
		if event.RecordedAt() < earliest {
			earliest = event.RecordedAt()
		}
	}
	return earliest, true
}

// citedIn is the sorted, distinct supporting orders that are held out.
func citedIn(supporting, holdout []string) []string {
	held := map[string]bool{}
	for _, decisionID := range holdout {
		held[decisionID] = true
	}
	var cited []string
	for _, orderID := range supporting {
		if held[orderID] {
			cited = append(cited, orderID)
			held[orderID] = false
		}
	}
	sort.Strings(cited)
	return cited
}

func evaluateDecision(decisionID string, ledger *outcomes.OutcomeLedger, replay Replay) (DecisionOutcome, error) {
	baselineRun, baselineVerdict, err := arm(decisionID, replay.BaselinePin, replay)
	if err != nil {
		return DecisionOutcome{}, err
	}
	treatmentRun, treatmentVerdict, err := arm(decisionID, replay.TreatmentPin, replay)
	if err != nil {
		return DecisionOutcome{}, err
	}
	verdict, err := verdictFor(decisionID, baselineRun, treatmentRun, replay)
	if err != nil {
		return DecisionOutcome{}, err
	}
	return NewDecisionOutcome(DecisionOutcomeSpec{
		DecisionID: decisionID, Verdict: verdict,
		BaselineValid: baselineVerdict.valid, TreatmentValid: treatmentVerdict.valid,
		BaselineCodes: baselineVerdict.codes, TreatmentCodes: treatmentVerdict.codes,
		BaselineRealisedRisk: realisedRisk(ledger, decisionID),
	})
}

// realisedRisk is the baseline's realised bad outcomes as currently recorded, sorted and
// distinct. Negative evidence only.
func realisedRisk(ledger *outcomes.OutcomeLedger, decisionID string) []string {
	seen := map[string]bool{}
	var risk []string
	for _, event := range ledger.CurrentView(decisionID) {
		kind := string(event.EventType())
		if notCounterfactual[event.EventType()] && !seen[kind] {
			seen[kind] = true
			risk = append(risk, kind)
		}
	}
	sort.Strings(risk)
	return risk
}

// arm runs one arm and validates it; the verdict is produced before any metric is read. A
// rejected arm has failed for this decision: its metrics are discarded rather than
// down-weighted, because there is no cost at which a rejected placement competes.
func arm(decisionID string, pin simulation.ScenarioVersionPin, replay Replay) (simulation.OrderRunResult, ValidationVerdict, error) {
	artifact, err := replay.Evaluator(decisionID, pin)
	if err != nil {
		return simulation.OrderRunResult{}, ValidationVerdict{}, err
	}
	verdict, err := replay.Validator(artifact.request, artifact.result)
	if err != nil {
		return simulation.OrderRunResult{}, ValidationVerdict{}, err
	}
	if verdict.valid {
		return artifact.run, verdict, nil
	}
	failed, err := simulation.NewOrderRunResult(simulation.OrderRunSpec{OrderID: decisionID,
		FailureReason: "independent validation rejected the placement: " + strings.Join(verdict.codes, ", ")})
	return failed, verdict, err
}

// verdictFor compares two valid arms with the same per-decision Pareto report the forward
// path uses -- one profile, two engines, never a blended delta.
func verdictFor(decisionID string, baselineRun, treatmentRun simulation.OrderRunResult, replay Replay) (string, error) {
	switch {
	case !baselineRun.Succeeded() && !treatmentRun.Succeeded():
		return Unpackable, nil
	case !treatmentRun.Succeeded():
		return Regressed, nil
	case !baselineRun.Succeeded():
		// The treatment packed a decision the baseline could not, and its packing passed the
		// validator: an improvement in the only sense available here.
		return Improved, nil
	}
	baseline, err := simulation.NewScenarioResult("baseline", replay.BaselinePin, []string{decisionID},
		[]simulation.OrderRunResult{baselineRun})
	if err != nil {
		return "", err
	}
	treatment, err := simulation.NewScenarioResult("treatment", replay.TreatmentPin, []string{decisionID},
		[]simulation.OrderRunResult{treatmentRun})
	if err != nil {
		return "", err
	}
	reports, err := simulation.CompareScenarios(baseline, treatment, replay.HigherIsBetter)
	if err != nil {
		return "", err
	}
	switch reports[0].Winner() {
	case "treatment":
		return Improved, nil
	case "baseline":
		return Regressed, nil
	}
	return TradedOff, nil
}

// listRepr is the reference's repr of a list of strings.
func listRepr(words []string) string {
	quoted := make([]string, len(words))
	for index, word := range words {
		quoted[index] = pyrepr.String(word)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
