# Changelog

What changed in the Go module `github.com/toxakara/packvium-go`, release by release. The
format follows [Keep a Changelog](https://keepachangelog.com/1.1.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
  and a command-line program: `go run github.com/toxakara/packvium-go/cmd/packvium < request.json`.
- Package `commerce`: quotes, eligibility policy and catalog versions, with `cmd/packvium-commerce`.
- Nine runnable examples under `examples/`, each checked by the test suite.

Requires Go 1.13 or newer. No dependencies outside the standard library.
