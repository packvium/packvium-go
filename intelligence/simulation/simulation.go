// Package simulation runs scenarios and compares them: what-if analysis over a fixed order
// corpus.
//
// A scenario runs an order corpus against one version pin -- an explicit, recorded set of
// catalog, tariff and policy versions, the same append-only version numbers the commerce
// registries hand out -- and records what happened, order by order, without changing
// anything those registries hold. Two scenarios, a baseline and a treatment, are compared only
// when they ran the identical order corpus, and the comparison is a Pareto report per order
// (package pareto), never a single blended number.
//
// The package does not itself price, validate or pack anything. RunScenario takes an
// OrderEvaluator the caller supplies -- a function that calls the registries and the packer --
// which keeps the orchestration apart from what it orchestrates. Given a deterministic
// evaluator, a scenario is reproducible from its own recorded pin and order ids alone.
package simulation

import (
	"fmt"
	"math"
	"sort"

	"github.com/packvium/packvium-go/intelligence/pareto"
	"github.com/packvium/packvium-go/internal/pyrepr"
)

// ScenarioError is the base of every scenario error this package returns that is not a plain
// argument error. errors.As matches MismatchedOrderCorpusError as one too.
type ScenarioError struct{ message string }

// Error is the message, in the reference's wording.
func (e *ScenarioError) Error() string { return e.message }

// MismatchedOrderCorpusError is a baseline and a treatment that did not run the identical
// order corpus, in the same order. Comparing them would not isolate the effect of the version
// pin, so it is refused rather than compared anyway.
type MismatchedOrderCorpusError struct{ message string }

// Error is the message, in the reference's wording.
func (e *MismatchedOrderCorpusError) Error() string { return e.message }

// Unwrap makes the error match *ScenarioError, its base in the reference.
func (e *MismatchedOrderCorpusError) Unwrap() error { return &ScenarioError{e.message} }

// NewMismatchedOrderCorpusError is the refusal every comparison of two scenarios shares.
func NewMismatchedOrderCorpusError() error {
	return &MismatchedOrderCorpusError{"baseline and treatment must have run the identical order corpus, in the same order"}
}

// PolicyVersion pins one policy rule to one version.
type PolicyVersion struct {
	RuleID  string
	Version int64
}

// VersionPinSpec is the caller's description of a pin. A nil field is a registry the scenario
// did not consult; there is no "current", so a stored result replays regardless of what any
// registry's history has grown to since.
type VersionPinSpec struct {
	CatalogVersion *int64
	TariffVersion  *int64
	PolicyVersion  *int64
	PolicyVersions []PolicyVersion
	SolverVersion  *string
}

// Version is a pointer to v, for the optional fields of a VersionPinSpec.
func Version(v int64) *int64 { return &v }

// ScenarioVersionPin is the exact, recorded configuration one scenario ran against.
type ScenarioVersionPin struct {
	catalogVersion, tariffVersion, policyVersion *int64
	policyVersions                               []PolicyVersion
	solverVersion                                *string
}

// NewScenarioVersionPin checks a pin in the reference's order and keeps the policy pins
// sorted by rule id.
func NewScenarioVersionPin(spec VersionPinSpec) (ScenarioVersionPin, error) {
	numeric := []*int64{spec.CatalogVersion, spec.TariffVersion, spec.PolicyVersion}
	if spec.CatalogVersion == nil && spec.TariffVersion == nil && spec.PolicyVersion == nil &&
		spec.SolverVersion == nil && len(spec.PolicyVersions) == 0 {
		return ScenarioVersionPin{}, fmt.Errorf("a version pin must set at least one of its fields")
	}
	for _, value := range numeric {
		if value != nil && *value <= 0 {
			return ScenarioVersionPin{}, fmt.Errorf("version numbers must be positive")
		}
	}
	seen := map[string]bool{}
	for _, pin := range spec.PolicyVersions {
		if pin.RuleID == "" || pin.Version <= 0 {
			return ScenarioVersionPin{}, fmt.Errorf("policy version pins require a rule id and positive version")
		}
	}
	for _, pin := range spec.PolicyVersions {
		if seen[pin.RuleID] {
			return ScenarioVersionPin{}, fmt.Errorf("a version pin cannot name the same policy rule twice")
		}
		seen[pin.RuleID] = true
	}
	if spec.SolverVersion != nil && *spec.SolverVersion == "" {
		return ScenarioVersionPin{}, fmt.Errorf("solver_version must be non-empty when supplied")
	}
	policies := append([]PolicyVersion(nil), spec.PolicyVersions...)
	sort.Slice(policies, func(i, j int) bool { return policies[i].RuleID < policies[j].RuleID })
	return ScenarioVersionPin{copyInt(spec.CatalogVersion), copyInt(spec.TariffVersion), copyInt(spec.PolicyVersion),
		policies, copyString(spec.SolverVersion)}, nil
}

func copyInt(value *int64) *int64 {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

// CatalogVersion is the pinned catalog version, or nil.
func (p ScenarioVersionPin) CatalogVersion() *int64 { return copyInt(p.catalogVersion) }

// TariffVersion is the pinned tariff version, or nil.
func (p ScenarioVersionPin) TariffVersion() *int64 { return copyInt(p.tariffVersion) }

// PolicyVersion is the pinned policy version, or nil.
func (p ScenarioVersionPin) PolicyVersion() *int64 { return copyInt(p.policyVersion) }

// PolicyVersions is the per-rule pins, sorted by rule id.
func (p ScenarioVersionPin) PolicyVersions() []PolicyVersion {
	return append([]PolicyVersion(nil), p.policyVersions...)
}

// SolverVersion is the pinned solver version, or nil.
func (p ScenarioVersionPin) SolverVersion() *string { return copyString(p.solverVersion) }

// Equal is whether two pins name the same versions.
func (p ScenarioVersionPin) Equal(other ScenarioVersionPin) bool {
	if len(p.policyVersions) != len(other.policyVersions) {
		return false
	}
	for index, pin := range p.policyVersions {
		if pin != other.policyVersions[index] {
			return false
		}
	}
	return sameInt(p.catalogVersion, other.catalogVersion) && sameInt(p.tariffVersion, other.tariffVersion) &&
		sameInt(p.policyVersion, other.policyVersion) && sameString(p.solverVersion, other.solverVersion)
}

func sameInt(a, b *int64) bool     { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func sameString(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// OrderRunSpec is the caller's description of one order's outcome. Metrics are Pareto axes --
// material cost, shipping cost, utilisation, a damage-risk proxy, runtime -- supplied by the
// evaluator; this package never computes or invents them. Artifact is whatever the evaluator
// wants retained with the run.
type OrderRunSpec struct {
	OrderID       string
	Succeeded     bool
	Metrics       map[string]float64
	FailureReason string
	Artifact      interface{}
}

// OrderRunResult is one order's outcome under one version pin.
type OrderRunResult struct {
	orderID       string
	succeeded     bool
	metrics       map[string]float64
	failureReason string
	artifact      interface{}
}

// NewOrderRunResult refuses a success with nothing measured, a failure without a reason, an
// unnamed metric and a non-finite one. Stricter than package pareto, which accepts
// infinities: a measurement of infinite cost is a broken evaluator, not an encoding of
// "unpriceable".
func NewOrderRunResult(spec OrderRunSpec) (OrderRunResult, error) {
	switch {
	case spec.OrderID == "":
		return OrderRunResult{}, fmt.Errorf("order_id is required")
	case spec.Succeeded && len(spec.Metrics) == 0:
		return OrderRunResult{}, fmt.Errorf("a succeeded run must report at least one metric")
	case !spec.Succeeded && spec.FailureReason == "":
		return OrderRunResult{}, fmt.Errorf("a failed run must record a failure_reason")
	}
	for name := range spec.Metrics {
		if name == "" {
			return OrderRunResult{}, fmt.Errorf("metric names must be non-empty")
		}
	}
	for _, value := range spec.Metrics {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return OrderRunResult{}, fmt.Errorf("metric values must be finite")
		}
	}
	return OrderRunResult{spec.OrderID, spec.Succeeded, copyMetrics(spec.Metrics), spec.FailureReason, spec.Artifact}, nil
}

func copyMetrics(metrics map[string]float64) map[string]float64 {
	copied := make(map[string]float64, len(metrics))
	for name, value := range metrics {
		copied[name] = value
	}
	return copied
}

// OrderID is the order the run is about.
func (r OrderRunResult) OrderID() string { return r.orderID }

// Succeeded is whether the evaluator produced a usable outcome.
func (r OrderRunResult) Succeeded() bool { return r.succeeded }

// Metrics is a copy of the run's metric axes; changing it changes nothing in the run.
func (r OrderRunResult) Metrics() map[string]float64 { return copyMetrics(r.metrics) }

// Metric is one metric and whether the run reported it.
func (r OrderRunResult) Metric(name string) (float64, bool) {
	value, ok := r.metrics[name]
	return value, ok
}

// FailureReason is why a failed run failed; empty for a success that gave none.
func (r OrderRunResult) FailureReason() string { return r.failureReason }

// Artifact is what the evaluator retained with the run, unchanged.
func (r OrderRunResult) Artifact() interface{} { return r.artifact }

// OrderEvaluator runs one order under one pin. It is supplied by the caller, which keeps
// pricing, policy evaluation and packing out of this layer; an error stops the scenario.
type OrderEvaluator func(orderID string, pin ScenarioVersionPin) (OrderRunResult, error)

// ScenarioResult is the complete record of one scenario run: the pin, the order corpus itself
// (not just its length) and every order's raw outcome, retained in full.
type ScenarioResult struct {
	scenarioID string
	pin        ScenarioVersionPin
	orderIDs   []string
	runs       []OrderRunResult
}

// NewScenarioResult refuses a result whose runs do not correspond one to one, in order, to
// its order ids.
func NewScenarioResult(scenarioID string, pin ScenarioVersionPin, orderIDs []string, runs []OrderRunResult) (ScenarioResult, error) {
	switch {
	case scenarioID == "":
		return ScenarioResult{}, fmt.Errorf("scenario_id is required")
	case len(orderIDs) == 0:
		return ScenarioResult{}, fmt.Errorf("order_ids cannot be empty")
	}
	matches := len(runs) == len(orderIDs)
	for index := 0; matches && index < len(runs); index++ {
		matches = runs[index].orderID == orderIDs[index]
	}
	if !matches {
		return ScenarioResult{}, fmt.Errorf("runs must correspond 1:1 to order_ids, in the same order")
	}
	return ScenarioResult{scenarioID, pin, append([]string(nil), orderIDs...), append([]OrderRunResult(nil), runs...)}, nil
}

// ScenarioID names the scenario.
func (s ScenarioResult) ScenarioID() string { return s.scenarioID }

// VersionPin is the pin the scenario ran against.
func (s ScenarioResult) VersionPin() ScenarioVersionPin { return s.pin }

// OrderIDs is the order corpus, in run order.
func (s ScenarioResult) OrderIDs() []string { return append([]string(nil), s.orderIDs...) }

// Runs is every order's outcome, in the order of OrderIDs.
func (s ScenarioResult) Runs() []OrderRunResult { return append([]OrderRunResult(nil), s.runs...) }

// SucceededCount is how many orders produced a usable outcome.
func (s ScenarioResult) SucceededCount() int {
	count := 0
	for _, run := range s.runs {
		if run.succeeded {
			count++
		}
	}
	return count
}

// FailedCount is how many orders did not.
func (s ScenarioResult) FailedCount() int { return len(s.runs) - s.SucceededCount() }

// Confidence is the fraction of orders that produced a usable outcome: a property of the
// result, always computed, rather than something each caller derives its own way.
func (s ScenarioResult) Confidence() float64 {
	return float64(s.SucceededCount()) / float64(len(s.runs))
}

// RawArtifact is the run recorded for one order. An unknown order is a *ScenarioError naming
// it, rather than an empty run that would push the mistake into a metrics lookup.
func (s ScenarioResult) RawArtifact(orderID string) (OrderRunResult, error) {
	for _, run := range s.runs {
		if run.orderID == orderID {
			return run, nil
		}
	}
	return OrderRunResult{}, &ScenarioError{fmt.Sprintf("no run recorded for order %s", pyrepr.String(orderID))}
}

// RunScenario runs every order through evaluator under pin, in order. Given a deterministic
// evaluator, two calls with the same arguments produce equal results: the reproducibility is
// this function's own determinism, not a separate mechanism.
func RunScenario(scenarioID string, pin ScenarioVersionPin, orderIDs []string, evaluator OrderEvaluator) (ScenarioResult, error) {
	runs := make([]OrderRunResult, 0, len(orderIDs))
	for _, orderID := range orderIDs {
		run, err := evaluator(orderID, pin)
		if err != nil {
			return ScenarioResult{}, err
		}
		runs = append(runs, run)
	}
	return NewScenarioResult(scenarioID, pin, orderIDs, runs)
}

// SameCorpus is whether two scenarios ran the identical order corpus, in the same order.
func SameCorpus(baseline, treatment ScenarioResult) bool {
	if len(baseline.orderIDs) != len(treatment.orderIDs) {
		return false
	}
	for index, orderID := range baseline.orderIDs {
		if treatment.orderIDs[index] != orderID {
			return false
		}
	}
	return true
}

// CompareScenarios compares two scenarios order by order through package pareto, never as a
// blended delta. Each order is its own profile, so a caller sees which orders improved,
// regressed or traded off, and "baseline" and "treatment" are the two engines compared in it.
// Only arms that succeeded are compared: a failed run has no metrics and is left out of that
// order's report. O(o) outside the report itself, pairing the runs in one pass.
func CompareScenarios(baseline, treatment ScenarioResult, higherIsBetter map[string]bool) ([]pareto.ProfileReport, error) {
	if !SameCorpus(baseline, treatment) {
		return nil, NewMismatchedOrderCorpusError()
	}
	var candidates []pareto.CandidateResult
	for index, baseRun := range baseline.runs {
		for _, arm := range []struct {
			name string
			run  OrderRunResult
		}{{"baseline", baseRun}, {"treatment", treatment.runs[index]}} {
			if !arm.run.succeeded {
				continue
			}
			candidate, err := pareto.NewCandidateResult(baseRun.orderID, arm.name, arm.run.metrics)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, candidate)
		}
	}
	return pareto.GenerateReport(candidates, higherIsBetter)
}
