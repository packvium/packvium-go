# Packvium for Go

Deterministic 3D cartonization and container packing in Go, with exact integer geometry and
no dependencies beyond the standard library.

Packvium for Go is a full reimplementation of the Packvium reference engine, not a binding
over another one. It accepts the same requests and returns the same results as the Python,
PHP, Java and .NET engines. Its tests run the shared conformance corpus, and every result
matches the reference result byte for byte.

> **Version 1.0.0.** This release corresponds to Packvium 1.0.0 in the other languages, and
> its tests run the 1.0.0 conformance corpus: every packing fixture and every commerce case.
> Field names, status codes and the objective vector stay the same for all of 1.x.

## Install

```bash
go get github.com/packvium/packvium-go@v1.0.1
```

Go 1.13 or newer. The module is checked on a real Go 1.13 toolchain, so projects that
cannot upgrade their compiler can still use it.

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

Fractional inches are stored exactly, without rounding to millimetres:

```go
domain.DimensionsOf("12 3/8", "8 1/2", "3/4", "in")
```

Every value object is immutable: a `*Spec` struct describes an item or container, the
`New...` constructor validates it, and the result exposes only accessors.

To get the result in the JSON format every other engine produces, use
`serialization.EncodeResult(result, "mm", "g")` with `jsonvalue`-style sorted output, or
skip the domain types entirely and call `serialization.Pack` on a decoded request.

## Examples

[`examples/`](examples) has one runnable program per directory. Each prints exactly what
its Python counterpart prints; `go test ./examples/` checks that, line by line.

| Example | What it shows |
| --- | --- |
| [`basic`](examples/basic) | The smallest useful call: items go in and placements come out. |
| [`objectives`](examples/objectives) | All six objectives, on scenes where they choose differently. |
| [`constraints`](examples/constraints) | Upright-only, floor-only, top-load limits and tags, and how to read why an item was refused. |
| [`units`](examples/units) | Why there are no floats: fractional inches, exact ticks, and a refusal caused by a single tick. |
| [`serialization`](examples/serialization) | The JSON contract, and which mistakes are refused and which are ignored. |
| [`shapes`](examples/shapes) | Items that do not fill their box: wedges sharing a crate as `convex_hull`, and a cushion that compresses until the crush limit refuses it. |
| [`nested`](examples/nested) | Units go into cartons and cartons go onto a pallet, in one call. |
| [`extensions`](examples/extensions) | Custom placement rules, item orders and candidate scorers plugged into the solver. |
| [`commerce`](examples/commerce) | Rate a shipment, apply an eligibility rule and pin a catalog version. |

```bash
go run ./examples/objectives
```

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

## What it does

- **Exact arithmetic.** Length is counted in ticks of 1/16000 mm and weight in ticks of
  1/8 µg. Coordinates are never floating-point numbers, so rounding cannot change where an
  item is placed.
- **Real constraints.** Weight and payload limits, allowed rotations, keep-upright,
  floor-only, non-stackable, top-load limits, minimum support ratio, incompatible tags,
  clearance, obstacles, axle loads and multi-stop routes.
- **Several solvers, not one.** Grid, layer, extreme-point, maximal-space, homogeneous-block
  and exact search for small problems.
- **Checked answers.** An independent validator checks every solution again, and every item
  left out is listed with a reason code.
- **Deterministic.** The same input always gives the same result. The only effect of the
  clock is the time limit you set; set an effort budget when you need bit-identical
  truncation too.
- **Commerce.** Package `commerce` prices a shipment, evaluates an eligibility policy and
  resolves catalog versions, as deterministic functions of the document you pass in.

## Differences from the reference engine

Valid requests get identical results. The Go port differs in a few places, none of which
shows up in the conformance corpus:

- Lengths and weights are 64-bit ticks, as in PHP and Java. A value that would overflow is
  refused, where Python would keep calculating with larger integers. Volumes are exact
  arbitrary-precision integers.
- The parallel portfolio cannot be enabled from a request, so it is not included; every
  start runs one after another, as the reference runs them.
- JSON `NaN`/`Infinity` literals, which the reference's parser accepts, are refused.

## Testing

```bash
go test ./...   # unit tests, and every example against its recorded output
```

The full conformance corpus runs in the Packvium suite repository, where all the engines are
checked against the same fixtures.

## License

MIT.
