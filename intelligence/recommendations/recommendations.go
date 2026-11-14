// Package recommendations proposes carton-catalog and rule changes, backed by evidence, and
// is the one route from a proposal to a published change.
//
// A recommendation is built from a baseline-versus-treatment comparison (package simulation)
// over a real order cohort. It names which orders support it, what changed and by how much
// per metric, how confident that evidence is, what constraints bound it and how to undo it.
// It is never an automatic change to the production catalog: Propose is handed no registry
// and cannot write to one. Only an explicit approval -- Approve with a caller-supplied
// publisher, or ApproveCatalog and ApprovePolicy with a commerce registry -- publishes.
//
// A recommendation with insufficient evidence is suppressed: Propose returns nil rather than
// an error a caller might mishandle, and rather than a recommendation with a misleadingly low
// confidence quietly attached.
package recommendations

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/commerce"
	"github.com/packvium/packvium-go/intelligence/simulation"
)

// RecommendationError is the base of recommendation errors that are not plain argument
// errors. The reference declares it for callers to match on; no path returns it yet.
type RecommendationError struct{ message string }

// Error is the message, in the reference's wording.
func (e *RecommendationError) Error() string { return e.message }

// ExpectedDelta is one metric's change from baseline to treatment, averaged over the orders
// both scenarios succeeded on; a failed order contributes to neither side.
type ExpectedDelta struct {
	metric                      string
	baselineMean, treatmentMean float64
}

// NewExpectedDelta refuses an unnamed metric.
func NewExpectedDelta(metric string, baselineMean, treatmentMean float64) (ExpectedDelta, error) {
	if metric == "" {
		return ExpectedDelta{}, fmt.Errorf("metric is required")
	}
	return ExpectedDelta{metric, baselineMean, treatmentMean}, nil
}

// Metric names the axis.
func (d ExpectedDelta) Metric() string { return d.metric }

// BaselineMean is the baseline's mean over the paired orders.
func (d ExpectedDelta) BaselineMean() float64 { return d.baselineMean }

// TreatmentMean is the treatment's mean over the same orders.
func (d ExpectedDelta) TreatmentMean() float64 { return d.treatmentMean }

// Delta is treatment minus baseline. Signed on purpose: whether a negative delta is good
// depends on the axis, which this type deliberately does not know.
func (d ExpectedDelta) Delta() float64 { return d.treatmentMean - d.baselineMean }

// RecommendationSpec is the content of one reviewable proposal.
type RecommendationSpec struct {
	RecommendationID   string
	Proposal           string
	SupportingOrderIDs []string
	ExpectedDeltas     []ExpectedDelta
	Confidence         float64
	Constraints        []string
	RollbackPlan       string
}

// Recommendation is one reviewable proposal. Existing changes nothing.
type Recommendation struct{ spec RecommendationSpec }

// NewRecommendation refuses a proposal without evidence, without bounds or without a way
// back.
func NewRecommendation(spec RecommendationSpec) (Recommendation, error) {
	switch {
	case spec.RecommendationID == "":
		return Recommendation{}, fmt.Errorf("recommendation_id is required")
	case spec.Proposal == "":
		return Recommendation{}, fmt.Errorf("proposal is required")
	case len(spec.SupportingOrderIDs) == 0:
		return Recommendation{}, fmt.Errorf("a recommendation must cite at least one supporting order")
	case len(spec.ExpectedDeltas) == 0:
		return Recommendation{}, fmt.Errorf("a recommendation must report at least one expected delta")
	case !(spec.Confidence >= 0 && spec.Confidence <= 1):
		return Recommendation{}, fmt.Errorf("confidence must be between 0 and 1")
	case len(spec.Constraints) == 0 || containsEmpty(spec.Constraints):
		return Recommendation{}, fmt.Errorf("a recommendation must state at least one non-empty constraint")
	case spec.RollbackPlan == "":
		return Recommendation{}, fmt.Errorf("a recommendation must state its rollback plan")
	}
	frozen := spec
	frozen.SupportingOrderIDs = append([]string(nil), spec.SupportingOrderIDs...)
	frozen.ExpectedDeltas = append([]ExpectedDelta(nil), spec.ExpectedDeltas...)
	frozen.Constraints = append([]string(nil), spec.Constraints...)
	return Recommendation{frozen}, nil
}

func containsEmpty(words []string) bool {
	for _, word := range words {
		if word == "" {
			return true
		}
	}
	return false
}

// RecommendationID names the proposal.
func (r Recommendation) RecommendationID() string { return r.spec.RecommendationID }

// Proposal is what the recommendation proposes.
func (r Recommendation) Proposal() string { return r.spec.Proposal }

// SupportingOrderIDs are the orders the evidence rests on, in corpus order.
func (r Recommendation) SupportingOrderIDs() []string {
	return append([]string(nil), r.spec.SupportingOrderIDs...)
}

// ExpectedDeltas are the per-metric changes, sorted by metric name.
func (r Recommendation) ExpectedDeltas() []ExpectedDelta {
	return append([]ExpectedDelta(nil), r.spec.ExpectedDeltas...)
}

// Confidence is the paired cohort's share of the whole corpus.
func (r Recommendation) Confidence() float64 { return r.spec.Confidence }

// Constraints are what bounds the proposal.
func (r Recommendation) Constraints() []string { return append([]string(nil), r.spec.Constraints...) }

// RollbackPlan is how to undo the change if it is approved.
func (r Recommendation) RollbackPlan() string { return r.spec.RollbackPlan }

// ApprovalRecord is the record of one approval: which recommendation, when, and which new
// registry version publishing it produced.
type ApprovalRecord struct {
	recommendationID string
	approvedAt       int64
	publishedVersion int64
}

// NewApprovalRecord refuses a negative instant and a version that is not positive.
func NewApprovalRecord(recommendationID string, approvedAt, publishedVersion int64) (ApprovalRecord, error) {
	switch {
	case recommendationID == "":
		return ApprovalRecord{}, fmt.Errorf("recommendation_id is required")
	case approvedAt < 0:
		return ApprovalRecord{}, fmt.Errorf("approved_at cannot be negative")
	case publishedVersion <= 0:
		return ApprovalRecord{}, fmt.Errorf("published_version must be positive")
	}
	return ApprovalRecord{recommendationID, approvedAt, publishedVersion}, nil
}

// RecommendationID is the approved recommendation.
func (a ApprovalRecord) RecommendationID() string { return a.recommendationID }

// ApprovedAt is the approval instant the caller supplied.
func (a ApprovalRecord) ApprovedAt() int64 { return a.approvedAt }

// PublishedVersion is the registry version publishing produced.
func (a ApprovalRecord) PublishedVersion() int64 { return a.publishedVersion }

// Evidence is the caller's bar for a proposal and the terms it carries. There is no default
// for MinimumCohortSize or MinimumConfidence: what counts as enough evidence is a product
// decision this package does not make on a caller's behalf.
type Evidence struct {
	Constraints       []string
	RollbackPlan      string
	MinimumCohortSize int
	MinimumConfidence float64
}

// Propose builds a recommendation from a baseline/treatment comparison, or returns nil when
// the evidence is too thin: a paired cohort below MinimumCohortSize, a confidence below
// MinimumConfidence, or no metric both arms reported. Two different corpora are a
// *simulation.MismatchedOrderCorpusError.
//
// A delta is a paired comparison, not the difference between two independently filtered
// populations: if the arms fail on different orders, averaging each arm's survivors
// separately can manufacture an improvement from cohort mix alone. Every metric is built from
// the same successful order pairs, and the evidence size is that paired cohort. O(o · k) time
// and space for o orders and at most k common metrics per order.
func Propose(recommendationID, proposal string, baseline, treatment simulation.ScenarioResult, evidence Evidence) (*Recommendation, error) {
	if !simulation.SameCorpus(baseline, treatment) {
		return nil, simulation.NewMismatchedOrderCorpusError()
	}
	if evidence.MinimumCohortSize <= 0 {
		return nil, fmt.Errorf("minimum_cohort_size must be positive")
	}
	if !(evidence.MinimumConfidence >= 0 && evidence.MinimumConfidence <= 1) {
		return nil, fmt.Errorf("minimum_confidence must be between 0 and 1")
	}
	paired := map[string]*[2][]float64{}
	var supporting []string
	treatmentRuns := treatment.Runs()
	for index, baselineRun := range baseline.Runs() {
		treatmentRun := treatmentRuns[index]
		if !baselineRun.Succeeded() || !treatmentRun.Succeeded() {
			continue
		}
		supporting = append(supporting, baselineRun.OrderID())
		for metric, value := range baselineRun.Metrics() {
			other, shared := treatmentRun.Metric(metric)
			if !shared {
				continue
			}
			values := paired[metric]
			if values == nil {
				values = &[2][]float64{}
				paired[metric] = values
			}
			values[0], values[1] = append(values[0], value), append(values[1], other)
		}
	}
	confidence := float64(len(supporting)) / float64(len(baseline.OrderIDs()))
	if len(supporting) < evidence.MinimumCohortSize || confidence < evidence.MinimumConfidence {
		return nil, nil
	}
	if len(paired) == 0 {
		// No metric both sides produced: nothing to recommend from.
		return nil, nil
	}
	metrics := make([]string, 0, len(paired))
	for metric := range paired {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	deltas := make([]ExpectedDelta, 0, len(metrics))
	for _, metric := range metrics {
		values := paired[metric]
		delta, err := NewExpectedDelta(metric, mean(values[0]), mean(values[1]))
		if err != nil {
			return nil, err
		}
		deltas = append(deltas, delta)
	}
	recommendation, err := NewRecommendation(RecommendationSpec{
		RecommendationID: recommendationID, Proposal: proposal, SupportingOrderIDs: supporting,
		ExpectedDeltas: deltas, Confidence: confidence, Constraints: evidence.Constraints,
		RollbackPlan: evidence.RollbackPlan,
	})
	if err != nil {
		return nil, err
	}
	return &recommendation, nil
}

// mean sums left to right, as the reference's sum() does, so the result is the same double.
func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

// Publisher makes the registry change an approval stands for and returns the version it
// produced.
type Publisher func() (int64, error)

// Approve is the generic path from a recommendation to a registry change: it calls publish
// exactly once and records the version it returns. Nothing in Propose can reach this.
func Approve(recommendation Recommendation, at int64, publish Publisher) (ApprovalRecord, error) {
	version, err := publish()
	if err != nil {
		return ApprovalRecord{}, err
	}
	return NewApprovalRecord(recommendation.RecommendationID(), at, version)
}

// ApproveCatalog publishes an approved catalog proposal through the real append-only
// registry, noting which recommendation it came from.
func ApproveCatalog(recommendation Recommendation, registry *commerce.CatalogRegistry, snapshot *commerce.CatalogSnapshot,
	approvedAt, effectiveAt int64) (ApprovalRecord, error) {
	version, err := registry.Publish(snapshot, big.NewInt(effectiveAt), big.NewInt(approvedAt),
		"approved recommendation "+recommendation.RecommendationID())
	if err != nil {
		return ApprovalRecord{}, err
	}
	return NewApprovalRecord(recommendation.RecommendationID(), approvedAt, int64(version.Number))
}

// PolicyChange is the rule an approved policy proposal publishes.
type PolicyChange struct {
	RuleID      string
	Scope       commerce.PolicyScope
	Action      commerce.PolicyAction
	Predicates  []commerce.PolicyPredicate
	Priority    int64
	EffectiveAt int64
}

// ApprovePolicy publishes an approved policy proposal through the real append-only registry
// as a new version of the rule, giving the recommendation as its reason.
func ApprovePolicy(recommendation Recommendation, registry *commerce.PolicyRegistry, change PolicyChange,
	approvedAt int64) (ApprovalRecord, error) {
	rule, err := registry.Publish(change.RuleID, commerce.PolicyRuleTerms{
		Scope: change.Scope, Action: change.Action, Predicates: change.Predicates,
		Priority: big.NewInt(change.Priority), EffectiveAt: big.NewInt(change.EffectiveAt),
		Reason: "approved recommendation " + recommendation.RecommendationID(),
	})
	if err != nil {
		return ApprovalRecord{}, err
	}
	return NewApprovalRecord(recommendation.RecommendationID(), approvedAt, int64(rule.Version))
}
