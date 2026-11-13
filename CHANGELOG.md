# Changelog

What changed in the Go module `github.com/packvium/packvium-go`, release by release. The
format follows [Keep a Changelog](https://keepachangelog.com/1.1.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
