# Changelog

What changed in the Go module `github.com/packvium/packvium-go`, release by release. The
format follows [Keep a Changelog](https://keepachangelog.com/1.1.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.3.0]

Answers every request the way Packvium 1.3.0 does in the other languages, checked byte for
byte against the 1.3.0 conformance corpus: 398 packing fixtures, 6 refusal cases, 55
commerce cases, the execution plan of every golden result and 419 operational-artifact
cases. Packing results and the plan of every golden result are unchanged from 1.2.0.

### Added

- **Operational artifacts.** Package `artifacts` builds `packvium-operational-artifact/v1`
  from a request, a validated result and optional loading orders (`artifacts.Build`, or
  `artifacts.FromJSON` from JSON text): the execution plan unchanged, container and box
  sizes as tick strings, the result's rendered values for a work order, and provenance --
  the request itself, the catalogs, the deterministic part of `result.algorithm` and whether
  a replay is guaranteed to be exact. `artifacts.ExportJSON` (RFC 8785),
  `artifacts.ExportCSV` (RFC 4180) and `artifacts.ExportWorkOrderHTML` (one self-contained
  page to print) are byte-identical to the other engines' exports. A refusal is an
  `*artifacts.Error` with a code from the closed set every engine shares. Like the plan, the
  package calls no solver, validator, renderer or clock.
- `examples/artifacts`, and `cmd/packvium-artifact-probe`, the batch probe the suite's
  artifact conformance run drives.

### Changed

- `execution.CanonicalPlanJSON` writes RFC 8785 canonical JSON, the form the artifact uses:
  keys in UTF-16 order, numbers as JavaScript writes them (`1.0` is `1`), U+2028 unescaped.
  The bytes of every plan an engine emits are unchanged; a plan carrying a number beyond
  2^53 - 1 or a string that is not UTF-8 is now refused with a `*execution.PlanError` whose
  new `Code` field names the rule.
- A pinned catalog, tariff or policy-rule version is found by its position instead of a scan
  of the history, and a two-axis Pareto report sorts once instead of comparing every pair of
  candidates. Answers are unchanged.

## [1.2.0]

Answers every request the way Packvium 1.2.0 does in the other languages, checked byte for
byte against the 1.2.0 conformance corpus: 398 packing fixtures, 6 refusal cases and 55
commerce cases. Packing results are unchanged from 1.1.0.

### Added

- **Execution plans.** Package `execution` turns a validated result into a plan an
  operator can follow (`execution.BuildPlan`, format `packvium-execution-plan/v1`): what the
  solver decided under `facts`, every sentence under `presentation` with the fields it
  cites, placements referenced by item type, orientation and exact tick position, runners-up
  explained by the first score index where they differ, and unplaced items with their proof
  level unchanged. A step order is used only when you pass one; without it the plan says
  `unavailable`. It calls no solver, no validator and no clock, and
  `execution.CanonicalPlanJSON` is byte-identical to the other engines' plan for each of the
  398 golden results.
- **Operator locks.** Package `execution/locks` re-solves a request with chosen placements
  pinned, read from the plan's own placement references (`locks.LocksFromPlan`) or built
  with `locks.NewPlacementLock`. A lock is an ordinary placement constraint on the usual
  solve path: it reserves its slot against other instances of its item type and can refuse
  nothing else, so the re-solve runs under the same independent validation and every policy
  rule the request carries. `locks.ResolveWithLocks` returns a separate result beside the
  approved one, reports whether each lock was honoured (`Preserved`, `Missing`), and refuses a
  self-contradictory lock set with `*locks.LockSetError` before any solve.
- `serialization.PackWithExtensions`, to pack a request with your own extensions; they are
  added to the constraints the request's `policy` compiles to, never substituted for them.
  `serialization.DecodeItems` reads a request's item types alone.
- **Scenario comparison and recommendations.** Packages under `intelligence/`:
  `intelligence/pareto` (dominance and a per-profile Pareto report that names a winner only
  when one result survives), `intelligence/simulation` (version-pinned scenarios over an
  order corpus, compared order by order), `intelligence/recommendations` (a proposal built
  from the orders both arms packed, suppressed below the evidence bar you set, published only
  through an explicit approval), `intelligence/outcomes` (an append-only ledger of shipment
  outcomes with a closed field list per event type and corrections that supersede) and
  `intelligence/holdout` (replay against decisions recorded after a split instant, with your
  independent validator judging every packing before any metric is compared). None of them
  reads the clock or packs anything itself.
- `commerce.NewCatalogRegistry`, `commerce.NewPolicyRegistry`, `commerce.NewPolicyPredicate`
  and the registries' `Publish` methods, so an approved recommendation publishes through the
  same append-only registries a commerce document loads into.
- `examples/execution`, which also pins a box with an operator lock and shows both ways a lock
  can fail, and `examples/intelligence`.

### Changed

- The beam keeps only the candidates it can use while it searches instead of sorting every
  feasible placement, and the top-load check reuses the settled load order of the placements
  already in the container. Results, counters and statistics are unchanged.
- The examples set a 60 s time limit as a safety fuse, so a busy machine cannot change
  their output.

## [1.1.0]

Answers every request the way Packvium 1.1.0 does in the other languages, checked byte for
byte against the 1.1.0 conformance corpus: 398 packing fixtures, 6 refusal cases and 55
commerce cases.

### Added

- **Container doors.** `container.access_directions` (`domain.ContainerSpec.AccessDirections`)
  names the walls a container is unloaded through. With doors stated, nothing due at a later
  stop may block an earlier item's way out through them. Doors are validated against the six
  faces and kept in canonical order; without them the rule stays off and results are as in
  1.0.x.

### Changed

- `container.pallet_overhang_limit` is reserved by the 1.1.0 contract and refused with
  `unsupported_feature` until an engine implements it.
- Faster candidate search: rules that cannot fire for an item are skipped once per search,
  and collision and surface queries use the spatial grid. Results are unchanged.

## [1.0.1]

### Changed

- **The module path is now `github.com/packvium/packvium-go`**, because the repository moved
  to the `packvium` organization. Change your imports from `github.com/toxakara/packvium-go`
  to the new path. The code and the results are identical to 1.0.0. Version 1.0.0 is
  still available under the old path, but it will not receive updates.

## [1.0.0]

First release of Packvium for Go. It is a full Go reimplementation that answers every request
the way Packvium 1.0.0 does in the other languages, checked byte for byte against the 1.0.0
conformance corpus: 396 packing fixtures, the refusal cases and 55 commerce cases.

### Added

- `packer.Packer` with the fast, balanced, quality and exact-small profiles, all six
  objectives, multi-container and nested packing, and an independent validator for every
  result.
- Exact lengths and weights (`units.Length`, `units.Weight`, `domain.DimensionsOf`) with no
  floating point in geometry.
- The JSON request and result contract (`serialization.Pack`, `serialization.EncodeResult`)
  and a command-line program: `go run github.com/packvium/packvium-go/cmd/packvium < request.json`.
- Package `commerce`: quotes, eligibility policy and catalog versions, with `cmd/packvium-commerce`.
- Nine runnable examples under `examples/`, each checked by the test suite.

Requires Go 1.13 or newer. No dependencies outside the standard library.
