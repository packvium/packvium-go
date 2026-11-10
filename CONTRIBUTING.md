# Contributing

Thanks for looking. This library is small, dependency-free and deliberately strict about
exactness. Please keep it that way.

## Getting set up

```bash
go vet ./... && go test ./...
```

The suite must be green before you change anything, so that any later failure is yours. It
runs every program in `examples/` as well as the unit tests.

## The rules that matter here

**Go 1.13 must keep working.** The module promises Go 1.13 or newer, and CI runs Go 1.13.
That rules out generics, `any`, the `min`/`max` builtins, range-over-int, the `slices`,
`maps` and `cmp` packages, `errors.Join`, `strings.Cut` and `os.ReadFile`. The `go`
directive only stops newer language features; a newer standard-library function compiles
locally and fails on 1.13, so run the suite on 1.13 before you open a pull request:

```bash
docker run --rm -v "$PWD":/src:ro -w /src -e GOCACHE=/tmp/c golang:1.13 sh -c 'go vet ./... && go test ./...'
```

**No floats in geometry or feasibility.** Every length is an integer number of ticks
(1/16000 mm) and every weight an integer number of 1/8 µg. If a change makes a placement
decision depend on binary floating point, it is wrong even if the tests pass.

**No dependencies outside the standard library.** A new one needs a very good argument.

**Inputs are immutable.** A pack call must never change the items or containers it was
given. Value objects have unexported fields, validating constructors and no setters.

**Determinism.** The same input must produce the same output. Go randomises map iteration:
never let a map's order reach a result or a tie-break; sort explicitly.

**Every fixed defect gets a regression test** that fails before the fix and passes after.

## Behavioural changes

This package is one of several independent implementations of the same documented
contract, and they must produce identical results for the same request. So:

- A change to solver behaviour, the objective vector, serialization field names or status
  semantics is a **cross-language change**. Open an issue describing it before writing
  code, so the other implementations move with it.
- A change confined to Go internals (typing, refactoring, performance without a change in
  output) is local and needs no coordination.

## Pull requests

- One logical change per pull request.
- Commit messages in imperative mood, under 72 characters:
  `type(scope): description` with `feat`, `fix`, `refactor`, `chore`, `docs` or `test`.
- `gofmt -l .` must print nothing, and `go vet ./... && go test ./...` must pass on Go 1.13
  and on the current Go release.
- Do not tag a version; releases are cut separately.

## How your feedback is handled

Every issue and discussion is read by a maintainer. This is what happens next:

1. **New issues are labelled `needs-triage`.** A maintainer answers with one of: a question
   back to you, a confirmation, or a reason it will not be taken on.
2. **A confirmed bug** gets the `bug` label and is reproduced from your request. A wrong result
   in one package is checked against the others, because all of them implement the same
   contract and must agree.
3. **An accepted feature request or idea** is added to the project's backlog, and the issue is
   labelled `accepted` with a comment saying so. An idea from Discussions is first turned into
   an issue and linked back to the discussion.
4. **When the change ships**, the issue is closed with the release it shipped in. A change to
   the request or result format can only ship in a release that is allowed to change it, so
   it may take more than one release.
5. **Declined** requests are closed with the reason, so you are never left waiting on a
   silent issue.

Questions go to **Discussions → Q&A**; security problems go to **Report a vulnerability**
under the Security tab, never to a public issue.
