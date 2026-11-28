# Packvium for Go

Deterministic 3D cartonization and container packing in Go, with exact integer geometry and
no dependencies beyond the standard library.

Packvium for Go is a full reimplementation of Packvium, not a binding over another engine.
It accepts the same requests as every Packvium engine. Python, PHP, Java, .NET and Go return identical results at the same version; Rust and JavaScript return a valid answer at or above the objective floor.
The reference engine is the Python package, `packvium`; every release of this module is
checked against its results byte for byte.

> **Version 1.7.0.** This release corresponds to Packvium 1.7.0 in the other languages, and
> it was checked against the 1.7.0 conformance corpus: every packing request, every commerce case, the
> execution plan and operational artifact of every expected result, every structured request
> error, every plan-revision case and every result-validation case. Field names, status codes and the objective vector stay
> the same for all of 1.x.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Packing with domain objects](#packing-with-domain-objects)
- [The JSON request API](#the-json-request-api)
- [Constraints and objectives](#constraints-and-objectives)
- [Boxes cut to measure, flat bags and stacking groups](#boxes-cut-to-measure-flat-bags-and-stacking-groups)
- [Commerce](#commerce)
- [Execution plans](#execution-plans)
- [Operator locks](#operator-locks)
- [Operational artifacts](#operational-artifacts)
- [Validating a result](#validating-a-result)
- [Fixed placements and plan revisions](#fixed-placements-and-plan-revisions)
- [Weight rebalancing](#weight-rebalancing)
- [Scenarios, recommendations and holdout replay](#scenarios-recommendations-and-holdout-replay)
- [Examples](#examples)
- [Command line](#command-line)
- [Determinism](#determinism)
- [Differences from the Python engine](#differences-from-the-python-engine)
- [Versions and milestones](#versions-and-milestones)
- [Testing](#testing)

## Install

```bash
go get github.com/packvium/packvium-go@v1.7.0
```

Go 1.13 or newer. The module is checked on a real Go 1.13 toolchain, so projects that
cannot upgrade their compiler can still use it. There are no third-party dependencies.

## Quick start

```go
package main

import (
	"fmt"
	"log"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
)

func main() {
	bookSize, _ := domain.DimensionsOf("210", "140", "30", "mm")
	boxSize, _ := domain.DimensionsOf("400", "300", "250", "mm")
	spec := domain.NewItemSpec("book", bookSize)
	spec.Quantity = 4
	book, err := domain.NewItem(spec)
	if err != nil {
		log.Fatal(err)
	}
	box, err := domain.NewContainer(domain.ContainerSpec{ID: "box", InnerDimensions: boxSize})
	if err != nil {
		log.Fatal(err)
	}
	balanced, _ := config.New(config.Default(config.Balanced))
	result, err := packer.New(balanced, algorithm.NoExtensions()).Pack([]*domain.Item{book}, []*domain.Container{box})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Status) // feasible
	for _, container := range result.Containers {
		for _, placement := range container.Placements() {
			// Coordinates are exact ticks: 1/16000 mm.
			fmt.Println(placement.Instance().ID(), placement.Position().X(), placement.Rotation())
		}
	}
}
```

## Packing with domain objects

The snippets from here on leave out the `if err != nil` checks to stay short; every call
that can fail returns an `error` as its last value.

Every value object is immutable: a `*Spec` struct describes an item or container, the
`New...` constructor validates it, and what comes back exposes only accessors. Lengths and
weights are parsed from text, so fractional inches are stored exactly, without rounding to
millimetres:

```go
mugSize, err := domain.DimensionsOf("4", "4", "5", "in")
weight, err := units.ParseWeight(units.TextQuantity("12 oz"), "g", units.Nearest)

mug := domain.NewItemSpec("mug", mugSize)
mug.Weight, mug.Quantity, mug.KeepUpright = weight, 2, true

payload, err := units.ParseWeight(units.TextQuantity("20 kg"), "g", units.Nearest)
box, err := domain.NewContainer(domain.ContainerSpec{
	ID: "box-m", InnerDimensions: boxSize, MaxPayload: &payload, CostMinor: 180,
})
```

`packer.New(cfg, extensions).Pack(items, containers)` returns a `*result.PackingResult`:
`Status` (such as `optimal`, `feasible`, `best_found` or `infeasible`), the packed
`Containers` with their placements, `Unpacked` with a reason code for every item left out,
and `Score`, the objective vector. `config.Default(profile)` gives the options for one of the
`config.Fast`, `config.Balanced`, `config.Quality` and `config.ExactSmall` profiles; change
fields on it, then call `config.New`. `serialization.EncodeResult(result, "mm", "g")` turns
the result into the JSON form every engine shares. [`examples/basic`](examples/basic) is the
whole program.

## The JSON request API

The same request every other Packvium engine takes can be packed without building domain
objects. Decode it with `encoding/json` into `map[string]interface{}` and pass it to
`serialization.Pack`; the result is the same JSON-shaped map the other engines return.

```go
var request map[string]interface{}
err := json.Unmarshal([]byte(`{
  "configuration": {"solver_profile": "balanced", "time_limit_ms": 60000},
  "items": [{"id": "book", "quantity": 4, "weight": "450 g",
             "dimensions": {"length": "210", "width": "140", "height": "30"}}],
  "containers": [{"id": "box",
                  "inner_dimensions": {"length": "400", "width": "300", "height": "250"}}]
}`), &request)
if err != nil {
	log.Fatal(err)
}
result, err := serialization.Pack(request)
if err != nil {
	log.Fatal(err) // a malformed request, refused with a message naming the problem
}
fmt.Println(result["status"]) // feasible
```

A request no engine may answer is refused with a `*domain.InvalidRequestError` that names
the bad value: `Code` is `invalid_request`, `Reason` one of a closed set (`missing_field`,
`wrong_type`, `below_minimum`, `above_maximum`, `negative_measure`, `invalid_unit`,
`duplicate_id`, `not_allowed`, `invalid_value`), `Field` the RFC 6901 pointer of the value,
and the message is the one every engine writes, such as
`invalid_request: /items/0/quantity: must be at least 1`. Every number below the minimum the
request schema gives it, and every negative length or weight, is refused this way.

```go
if refusal, ok := err.(*domain.InvalidRequestError); ok {
	fmt.Println(refusal.Reason, refusal.Field) // below_minimum /items/0/quantity
}
```

The request schema closes `configuration` and its `effort_budget`, and so does the engine: a
key they do not declare is refused with reason `not_allowed` and its own pointer, never
ignored, so a misspelt `profile` for `solver_profile` or `top_k` for `alternatives` no longer
runs on defaults. When several keys are unknown, the first in code point order is named.
`parallel` is refused the same way: running starts concurrently is an execution setting, not
part of the request. Every closed vocabulary of the schema is checked the same way: an unknown
`objective`, `configuration.solvers` name, output or dimensional-weight unit code,
`allowed_rotations` code, `shape_type`, `ground_contact_rule` or `access_directions` wall is
`not_allowed` with its pointer. Output and dimensional-weight units must be one of the
lower-case codes, and `ticks` is not one; a measure's own unit (`units.length`, `"10 cm"`,
`{"unit": " KG "}`) is matched ignoring case and surrounding space. A `nesting_height` not below the item's own height is
`invalid_value` at its pointer. With no `output` block, a result is written in the request's
`units.length`, spelt as its schema code.

```go
_, err = serialization.Pack(map[string]interface{}{
	"configuration": map[string]interface{}{"profile": "balanced"},
	"items":         []interface{}{},
	"containers":    []interface{}{},
})
var refusal *domain.InvalidRequestError
if errors.As(err, &refusal) {
	fmt.Println(refusal.Reason, refusal.Field) // not_allowed /configuration/profile
	fmt.Println(refusal)                       // invalid_request: /configuration/profile: is not a known field
}
```

A request with several mistakes is refused for the first in one documented order: an
unsupported field, the units, the output units, the configuration, each item, the item ids,
each container, the container ids, the fixed placements.

The other refusals keep their own types: `*serialization.UnsupportedFeatureError`,
`*algorithm.UnknownSolverError` and `*algorithm.UnknownObjectiveError` (for a configuration
built in Go; a JSON request names them as `not_allowed`), and `*policy.PolicyError`.

A request built from domain objects is held to the same rules. A constructor that refuses
its own value (`domain.NewItem`, `domain.NewContainer`, `config.New`, a measure) returns a
`*domain.ObjectRefusal` whose `Reason` is the schema's reason for the value and whose message
is the detail alone, because the object does not yet know where in a request it will sit.
`Packer.PackWithFixedPlacements` then checks the whole request in the JSON order and refuses
with the JSON path's `*domain.InvalidRequestError`, field and message included: a quantity or
count past 2^53 - 1, an empty stacking group name, a repeated id, a fixed placement whose
container instance is below 1.
[`examples/errors`](examples/errors) turns each kind into the response a web handler
would send.

A request that sets a field no engine implements yet (`container.pallet_overhang_limit`) is
refused with `unsupported_feature` rather than answered as though the field were absent.
Numbers decoded by `encoding/json` are `float64`, which cannot hold integers above 2^53
exactly; quantities, dimensions and weights in a request are small or written as strings,
and the `cmd/packvium` program decodes every integer exactly.
[`examples/serialization`](examples/serialization) shows the contract, and which mistakes
are refused and which are ignored.

## Constraints and objectives

Most packing rules are fields on `domain.ItemSpec` and `domain.ContainerSpec`:

```go
paint.KeepUpright = true                                // never tipped over
glass.MustBeOnFloor, glass.MaxTopLoad = true, &topLoad  // nothing heavier than topLoad on it
cake.Stackable = false                                  // nothing on it at all
bleach.Tags, bleach.IncompatibleTags = []string{"hazmat"}, []string{"food"}
```

Weight and payload limits, allowed rotations, minimum support ratio, clearance, obstacles,
axle loads, stop order on multi-stop routes and container doors (`AccessDirections`) work
the same way. A rule with no field is a custom `constraint.PlacementConstraint`, registered
through `algorithm.ExtensionsWithConstraints`; [`examples/extensions`](examples/extensions)
shows one, and why a rule every caller must obey belongs in the request instead. A request's
`policy` block compiles into the same constraint pipeline, so an illegal placement is
refused during the search rather than filtered out afterwards.

An objective decides what "best" means. There are seven: `default`, `lowest_cost`,
`shipping_cost`, `lowest_landed_cost`, `open_dimension_height`, `maximum_value` and
`maximum_volume`. `maximum_volume` ranks the share of the requested volume left behind ahead
of the number of pieces, the way container-loading benchmarks are scored: where `default`
keeps many small items, it keeps the one large item that fills more space.

```go
options := config.Default(config.Balanced)
options.Objective = "lowest_cost"
cfg, err := config.New(options)
```

The score is a lexicographic vector of exact integers. Its first key is the number of items
left out, so no objective will leave an item behind to save money; under `maximum_volume` it
is the share of the requested volume left behind, and a complete answer still beats every
incomplete one. Every item that
is left out carries a reason code and a proof level:

```go
for _, left := range result.Unpacked {
	fmt.Println(left.Instance.ID(), left.Reason, left.Proof.Level)
}
```

[`examples/constraints`](examples/constraints) and [`examples/objectives`](examples/objectives)
build loads where each rule changes the answer, and so does each of six of the seven
objectives (all but `maximum_volume`).

With the default `MinimumSupportRatio` of 0 an item may stand above the floor with nothing
under it. Such a result ends its `Warnings` with one line, the same in every engine, so a
caller who never set the ratio is told:

```go
for _, warning := range result.Warnings {
	fmt.Println(warning)
	// unsupported_placements: 1 placement(s) rest on nothing above the container floor;
	// set configuration.minimum_support_ratio above 0 to require support
}
```

An item left out because no container of a fitting type is left reports
`container_inventory_exhausted`; one that overloads an axle wherever it stands alone reports
`axle_overloaded`. Both are `inferred`.

## Boxes cut to measure, flat bags and stacking groups

A **made-to-measure** container is an on-demand box machine. Its `InnerDimensions` is the
largest box it cuts; `domain.NewMadeToMeasure` adds the smallest box and the cut step per
axis, the board price per square metre and the board weight per square metre, and the outer
allowance dimensional weight is billed on. Each trial packs into up to four boxes sized from
the items, smallest first, and keeps the first that takes everything, so the box is compact
instead of a row or a plate:

```go
mm := units.TicksPerMM
machine, err := domain.NewMadeToMeasure(
	[3]int64{60 * mm, 60 * mm, 60 * mm}, // the smallest box
	[3]int64{25 * mm, 25 * mm, 25 * mm}, // the cut step
	1000,                                // board, minor units per square metre
	0,                                   // board weight, weight ticks per square metre
	[3]int64{3 * mm, 3 * mm, 3 * mm})    // board thickness on each side
cutter, err := domain.NewContainer(domain.ContainerSpec{ID: "machine", CostMinor: 40,
	InnerDimensions: domain.MustDimensions(1510*mm, 1510*mm, 1510*mm), MadeToMeasure: machine})
```

The packed container is the box that ships: its `InnerDimensions`, `OuterDimensions`,
`CostMinor` and `TareWeight` are the fabricated box's, every objective prices it, and
`AchievedDimensions()` is the extent of its contents before it was rounded onto the machine
grid. A JSON request that gives such a container axles, obstacles or a void-fill reserve, or
fixes a placement in it, is refused as an unsupported feature.

A **flat bag** reads `InnerDimensions` as a poly mailer's flat length, flat width and largest
filled thickness, and admits a packing only when length plus thickness and width plus
thickness of the load fit the bag. Its dimensional weight is billed on the load's
`AchievedDimensions()`, and bags and boxes mix in one plan:

```go
mailer, err := domain.NewContainer(domain.ContainerSpec{ID: "mailer", DeformableBoundary: domain.FlatBag,
	InnerDimensions: domain.MustDimensions(240*mm, 140*mm, 100*mm)})
```

**Stacking groups** say which item may rest directly on which: "pallets may carry pallets
and cartons, never drums". An item with no group may not rest on a constrained one, every
supporter of a bridging item must accept it, and `Stackable = false` still wins:

```go
pallet := domain.NewItemSpec("pallet", palletSize)
group := "pallet"
pallet.StackingGroup, pallet.AcceptsStackingGroups = &group, []string{"pallet", "carton"}
```

In a JSON request these are `container.made_to_measure`, `container.deformable_boundary:
{"kind": "flat_bag"}`, `item.stacking_group` and `item.accepts_stacking_groups`, and the
result's containers carry `achieved_dimensions` and `made_to_measure.{cost_minor,
tare_weight}`.

## Commerce

Package `commerce` prices a shipment, evaluates an eligibility policy and resolves catalog
versions. Each of the three functions takes one commerce document (your published tariffs,
policy rules and catalog versions) and one request, and returns a result document:

```go
quote, err := commerce.Quote(document, map[string]interface{}{
	"carrier_id": "acme", "service_id": "ground", "tariff_version": 1,
	"zone": "zone-a", "actual_weight_g": 1200, "volume_mm3": 6000000,
	"requested_accessorials": []interface{}{"liftgate"},
})
```

`commerce.EvaluatePolicy` and `commerce.CatalogVersionInfo` work the same way. A request
the commercial model is entitled to refuse is not an error: it is a result whose `status`
is `rejected` with a code from `commerce.RejectionCodes`. Every "which version applies"
question is answered from a pin or an instant you supply, never from the clock.
[`examples/commerce`](examples/commerce) walks through all three.

## Execution plans

A packing result says where every box goes; an operator needs a work order. Package
`execution` derives one from a validated result: what the solver decided under `facts`, the
sentence a screen shows under `presentation` together with the fields it `cites`,
placements referenced by item type, orientation and exact position, and every unplaced item
with its proof level carried through unsoftened.

```go
result, err := serialization.Pack(request)
plan, err := execution.BuildPlan(request, result, nil)
fmt.Println(plan["format"]) // packvium-execution-plan/v1

// A loading order is injected, never guessed: container 0's placements, in lifting order.
ordered, err := execution.BuildPlan(request, result, map[int][]int{0: order})

canonical, err := execution.CanonicalPlanJSON(plan)
```

Without a loading order the plan says `"order": "unavailable"` rather than presenting the
solver's search order as a safe one; with one, which must be a permutation of the
container's placements, each step is numbered. The package imports no solver, no validator
and no clock, and `CanonicalPlanJSON` is byte-identical to the other engines' plan of the
same result. Since 1.3.0 that form is RFC 8785, the canonical JSON the operational artifact
uses. [`examples/execution`](examples/execution) prints a whole plan.

## Operator locks

An operator standing at the truck can pin a box: "this printer stays here". Package
`execution/locks` turns that into a second plan, never an edited one. A lock is read from the
plan's own placement reference and becomes an ordinary placement constraint, so the re-solve
runs the same solvers under the same independent validation; a lock can forbid alternatives
to itself and nothing else.

```go
pinned, err := locks.LocksFromPlan(plan, 0, []string{"printer"})
resolved, err := locks.ResolveWithLocks(request, pinned, nil)
if !resolved.Preserved() {
	for _, missed := range resolved.Missing() {
		fmt.Println("not honoured:", missed.ItemType(), missed.PositionTicks())
	}
}
// resolved.Result() is a separate result; the approved one is untouched.
```

A lock the solve cannot honour (unsupported, overloaded, out of reach) is an answer:
`Preserved()` is false and `Missing()` names it, and the result is the engine's ordinary
answer, with any item it could not place listed with its usual reason. A lock set that contradicts itself (two locks claiming the same
space, or an item type the request does not contain) is a `*locks.LockSetError`, returned
before any solve. The third argument keeps your own extensions; the request's `policy` rules
still apply. `locks.NewPlacementLock(containerIndex, itemType, orientation, position)` builds
a lock by hand.

## Operational artifacts

A warehouse or transport system that receives a plan still cannot draw it, print it or say
where it came from without calling the engine again. Package `artifacts` builds the one
document that can: the plan unchanged, the container and box sizes in ticks, the result's
rendered values for a work order, and provenance -- the request itself, the catalogs, the
deterministic part of `result.algorithm` and whether a replay is guaranteed to be exact.

```go
// order is a loading order from sequence.SafeLoadingOrder, as examples/artifacts computes it.
artifact, err := artifacts.Build(request, result, map[int][]int{0: order})
fmt.Println(artifact["format"]) // packvium-operational-artifact/v1

canonical, err := artifacts.ExportJSON(artifact)     // RFC 8785, the bytes every engine emits
csv, err := artifacts.ExportCSV(artifact)            // RFC 4180, one row per step and unplaced item
html, err := artifacts.ExportWorkOrderHTML(artifact) // a self-contained page to print

if _, err := artifacts.ExportCSV(map[string]interface{}{"format": "v2"}); err != nil {
	fmt.Println(err.(*artifacts.Error).Code) // unknown_format
}
```

A refusal is an `*artifacts.Error` whose `Code` is one of a closed set every engine shares:
`invalid_request`, `invalid_result`, `invalid_plan_input`, `mixed_units`,
`number_out_of_range`, `invalid_string`, `invalid_value`, `unknown_format` and
`invalid_json`. Numbers beyond 2^53 - 1 are refused, because JavaScript would already hold
a different one. `artifacts.Parse` and `artifacts.FromJSON` read JSON text with integers kept
exact; decode with them rather than with `encoding/json` alone, which turns an escaped lone
surrogate (`"\ud800"`) into U+FFFD where the other engines refuse it. Exports read the
artifact through its canonical bytes, so a `float64` `1.0` prints as the `1` every other
engine reads back. `artifacts.ReplayLevel(result)` answers `exact` or `not_guaranteed`, the
promise an artifact of that result would make, without building one.
[`examples/artifacts`](examples/artifacts) builds and exports one.

## Validating a result

`validation.ValidateResult(request, result)` judges any engine's result against its request,
both as JSON text, and returns the issues the reference validator returns for the same
documents: the same codes, the same details, in the same order. It shares no code with the
solver: units, geometry, support, load and the objective vector are recomputed from the two
documents, so it catches a defect inside the engine as well as a hand-edited result. An empty
list means valid. Documents it cannot judge at all are refused with a
`*validation.MalformedResult` whose `Kind` is `contract` (the result lacks the documented
shape) or `shape` (a value of the wrong type, text that is not JSON).

```go
request := []byte(`{"items": [{"id": "cube", "dimensions": {"length": "100", "width": "100", "height": "100"}}],
  "containers": [{"id": "box", "inner_dimensions": {"length": "200", "width": "200", "height": "200"}}]}`)
var decoded interface{}
if err := json.Unmarshal(request, &decoded); err != nil {
	log.Fatal(err)
}
packed, err := serialization.Pack(decoded)
if err != nil {
	log.Fatal(err)
}
result, err := json.Marshal(packed)
if err != nil {
	log.Fatal(err)
}
issues, err := validation.ValidateResult(request, result)
fmt.Println(len(issues), err) // 0 <nil>
```

## Fixed placements and plan revisions

A request's `fixed_placements` pins items that are already loaded, or locked in place,
before the solve: item type, container type and instance, the physical origin a result
reports, and an orientation. Fixed items take the first instances of their type, their
containers open first and are always kept, and the solve packs around them with their weight,
support and top load counted. They come back marked `"fixed": true`, and the independent
validator refuses an answer that drops, moves or invents one. A fixed set that is not a valid
packing on its own is refused before any search with a `*domain.InvalidRequestError` whose
`Code` is `invalid_fixed_placement`, reason `malformed` for an entry that is not the schema's
shape (nothing is coerced) or `cannot_hold`. With domain objects the same is
`packer.New(cfg, nil).PackWithFixedPlacements(items, containers, fixed)`.

Package `revisions` records what changed against an approved plan as
`packvium-plan-revision/v1`, an append-only chain: each revision names its parent and the
artifact it replaced by SHA-256 over RFC 8785 bytes, records the events observed against
that artifact (`item_missing`, `container_substituted`, `placement_locked`,
`placement_verified`) and carries the request they produce. The replan is an ordinary solve of
that request; locks and verified placements become fixed placements.

```go
root, err := revisions.RootRevision(request)
next, err := revisions.DeriveRevision(root, approvedArtifact, events)
issues, err := revisions.VerifyRevisionChain([]interface{}{root, next}, []interface{}{nil, approvedArtifact})
digest, err := revisions.DocumentDigest(next) // "sha256:..."
```

A refusal is a `*revisions.Error` with a code every engine shares (`invalid_revision`,
`invalid_event`, `event_conflict`, `invalid_artifact`, or a canonical-form code). The
package calls no solver, validator or clock. `intelligence/revisionoutcomes` turns a
revision's events into outcome-ledger events, so what happened on the dock becomes evidence
for the historical evaluation. [`examples/revisions`](examples/revisions) replans a
half-loaded job.

## Weight rebalancing

The packer opens as few containers as it can and fills the first one tightly. When the
containers should weigh about the same -- two pallets on a forklift -- run
`packer.RebalanceWeight` on the finished packing. It moves one item at a time from the
heaviest container to a lighter one, and keeps a move only when the item has a real place in
its new container and the whole packing still passes the independent validator: it never
adds a container, drops an item or leaves one resting on a gap, and fixed placements stay
where they are. It stops when no single move narrows the gap, after `MaxMoves` (64 by
default) or at `TimeLimitMS`; it is greedy, so the split is better, not necessarily the most
even one possible.

```go
result, err := packer.New(cfg, nil).Pack(items, pallets)
request, err := domain.NewPackingRequest(items, pallets)
rebalanced, err := packer.RebalanceWeight(request, result.Containers, result.Unpacked, cfg,
	packer.DefaultRebalanceOptions())
for _, move := range rebalanced.Moves {
	fmt.Println(move.ItemID, move.FromContainerID, "->", move.ToContainerID) // paint-20l#6 pallet#1 -> pallet#2
}
```

Under a landed-cost objective it refuses what `Pack` refuses, and never makes a move that
prices a container past its rate table. [`examples/rebalancing`](examples/rebalancing) evens
out two pallets.

## Scenarios, recommendations and holdout replay

The packages under `intelligence/` answer the question that comes after packing well: is a
new carton set, tariff or rule actually an improvement? None of them returns a single
blended score, reads the clock or packs anything itself; your code supplies the evaluation
as a function.

```go
baselinePin, _ := simulation.NewScenarioVersionPin(simulation.VersionPinSpec{CatalogVersion: simulation.Version(1)})
treatmentPin, _ := simulation.NewScenarioVersionPin(simulation.VersionPinSpec{CatalogVersion: simulation.Version(2)})

// evaluate(orderID, pin) packs one order under one pin and returns its metrics.
baseline, err := simulation.RunScenario("current-cartons", baselinePin, orderIDs, evaluate)
treatment, err := simulation.RunScenario("proposed-cartons", treatmentPin, orderIDs, evaluate)

higherIsBetter := map[string]bool{"cost_minor": false, "utilisation": true}
reports, err := simulation.CompareScenarios(baseline, treatment, higherIsBetter)
for _, report := range reports {
	fmt.Println(report.Profile(), report.Winner(), report.ParetoOptimal())
}
```

- **`intelligence/pareto`** decides dominance: no worse on every axis and strictly better on
  one. A genuine trade-off has no winner, and the report says so.
- **`intelligence/simulation`** runs an order corpus under a version pin and compares two
  scenarios order by order; two different corpora are refused.
- **`intelligence/recommendations`** proposes a change from the orders both arms packed
  (`recommendations.Propose`), returns nil when the evidence is below the bar you set, and
  publishes only through an explicit approval: `Approve`, `ApproveCatalog` or
  `ApprovePolicy`, the last two through the commerce registries.
- **`intelligence/outcomes`** is an append-only ledger of what happened to each shipment,
  with a closed field list per event type and corrections that supersede rather than
  overwrite.
- **`intelligence/holdout`** replays a recommendation against decisions recorded after a
  split instant you choose. Your independent validator judges every packing before any
  metric is compared, a rejected packing never counts as an improvement, and a
  recommendation that cites a held-out decision is refused.

```go
evaluation, err := holdout.EvaluateOnHistory(*recommendation, ledger, holdout.Replay{
	DecisionIDs: decisionIDs, BaselinePin: baselinePin, TreatmentPin: treatmentPin,
	Evaluator: replay, Validator: validate, HigherIsBetter: higherIsBetter,
}, 500)
fmt.Println(evaluation.Improved(), evaluation.Regressed(), evaluation.TradedOff(), evaluation.Unpackable())
```

[`examples/intelligence`](examples/intelligence) runs the whole story on five orders and
four historical shipments.

## Examples

[`examples/`](examples) has one runnable program per directory. `go test ./examples/` is the
test suite's run of every example: it checks each one's output against a recorded copy; the
output uses the notation shared by every Packvium language.

| Example | What it shows | Run it |
| --- | --- | --- |
| [`basic`](examples/basic) | The smallest useful call: items go in and placements come out. | `go run ./examples/basic` |
| [`objectives`](examples/objectives) | Six of the seven objectives (all but `maximum_volume`), each on a load where it chooses differently. | `go run ./examples/objectives` |
| [`constraints`](examples/constraints) | Upright-only, floor-only, top-load limits and tags, and how to read why an item was refused. | `go run ./examples/constraints` |
| [`units`](examples/units) | Why there are no floats: fractional inches, exact ticks, and a refusal caused by a single tick. | `go run ./examples/units` |
| [`serialization`](examples/serialization) | The JSON contract, and which mistakes are refused and which are ignored. | `go run ./examples/serialization` |
| [`shapes`](examples/shapes) | Items that do not fill their box: wedges sharing a crate as `convex_hull`, and a cushion that compresses until the crush limit refuses it. | `go run ./examples/shapes` |
| [`nested`](examples/nested) | Units go into cartons and cartons go onto a pallet, in one call. | `go run ./examples/nested` |
| [`extensions`](examples/extensions) | Custom placement rules, item orders and candidate scorers plugged into the solver. | `go run ./examples/extensions` |
| [`commerce`](examples/commerce) | Rate a shipment, apply an eligibility rule and pin a catalog version. | `go run ./examples/commerce` |
| [`artifacts`](examples/artifacts) | Hand a result to a warehouse system: one document with geometry and provenance, a CSV, a printable work order. | `go run ./examples/artifacts` |
| [`execution`](examples/execution) | Turn a result into a work order, then pin a box with an operator lock and see both ways a lock can fail. | `go run ./examples/execution` |
| [`revisions`](examples/revisions) | Replan a half-loaded job: keep a loaded box where it is, record a missing item against the approved plan, and audit the chain. | `go run ./examples/revisions` |
| [`intelligence`](examples/intelligence) | Compare two carton sets order by order, propose the change, and replay it against history it has not seen. | `go run ./examples/intelligence` |
| [`errors`](examples/errors) | Turn a refused request into a useful answer, the way a web handler would: one reason, one pointer, one message. | `go run ./examples/errors` |
| [`fixed_placements`](examples/fixed_placements) | Pack a trailer that is already partly loaded, and what happens when the record of what is on board is impossible. | `go run ./examples/fixed_placements` |
| [`limits`](examples/limits) | Obstacles, item caps, tag limits, eligible containers and a fleet cap on a courier's van and bikes. | `go run ./examples/limits` |
| [`rebalancing`](examples/rebalancing) | Even out the weight across pallets after packing. | `go run ./examples/rebalancing` |
| [`reproducibility`](examples/reproducibility) | The same answer on every machine: an effort budget against a wall-clock limit. | `go run ./examples/reproducibility` |
| [`trucking`](examples/trucking) | A delivery van with axle ratings, rear doors and a three-stop route, and the loading order a driver follows. | `go run ./examples/trucking` |

Run them from the module root.

## Command line

`cmd/packvium` reads a JSON packing request (a file argument, or standard input) and writes
the result JSON to standard output, or to `-o FILE`. The formats are the ones every other
Packvium engine uses.

```bash
go build -o packvium ./cmd/packvium
echo '{"items":[{"id":"box","quantity":8,"dimensions":{"length":"50","width":"50","height":"50"}}],"containers":[{"id":"carton","inner_dimensions":{"length":"100","width":"100","height":"100"}}]}' | ./packvium
```

A refused request exits with status 1 and prints the reason on standard error, in the same
wording as the other engines; a usage error exits with status 2. `cmd/packvium-commerce`
is the same for the commerce API.

## Determinism

The same input always gives the same result.

- **Exact arithmetic.** Length is counted in ticks of 1/16000 mm and weight in ticks of
  1/8 µg. Coordinates are never floating-point numbers, so rounding cannot change where an
  item is placed. Volumes are exact arbitrary-precision integers.
- **No hidden inputs.** The only random source is a seeded generator (`seed` in the
  configuration) that every engine implements identically, and no output depends on map
  iteration order. The only effect of the clock is the time limit you set: a generous
  `time_limit_ms` is a safety fuse, and an `effort_budget` gives bit-identical truncation
  where a limit is reached.
- **Checked answers.** An independent validator checks every solution again before it is
  returned.
- **No clock above the packer.** Execution plans, commerce answers, locks, scenarios,
  recommendations and holdout replays are functions of what you pass in; every "as of" is an
  instant you supply.

## Differences from the Python engine

Valid requests get identical results. This module differs from the Python package in a few
places, none of which shows up in the conformance corpus:

- Lengths and weights are 64-bit ticks, as in PHP and Java. A value that would overflow is
  refused, where Python would keep calculating with larger integers.
- There is no parallel portfolio: starts always run one after another, and a request cannot ask for parallelism in any engine.
- JSON `NaN`/`Infinity` literals, which Python's parser accepts, are refused.
- Errors are returned values where Python raises exceptions, and a Go type makes
  some malformed values impossible to pass at all, such as a lock position without three
  integer axes or a boolean metric.

## Versions and milestones

The module's versions follow the Packvium releases: 1.7.0 here answers every request the way
Packvium 1.7.0 does in the other languages. Within 1.x, field names, status codes and the
objective vector do not change. What each release added is in [CHANGELOG.md](CHANGELOG.md).

## Testing

```bash
go vet ./...
go test ./...   # unit tests, and every example against its recorded output
```

Before each release the module is also run against the full conformance corpus, the shared
requests and expected results every Packvium engine is checked against. API documentation is on
[pkg.go.dev](https://pkg.go.dev/github.com/packvium/packvium-go). To contribute or report a
problem, see [CONTRIBUTING.md](CONTRIBUTING.md); for security reports, [SECURITY.md](SECURITY.md).

## License

MIT.
