# Security policy

## Supported versions

Only the latest `1.x` release receives fixes. There is no long-term support branch.

## Reporting a vulnerability

Please report privately, not in a public issue. Use GitHub's **Report a vulnerability**
button under the Security tab, which opens a private advisory.

Include the version, the Go version, a minimal request that reproduces the problem, and
what you observed. A crash, a hang or a wildly wrong result on a small input is worth
reporting even if you are unsure it is a security matter.

You can expect an acknowledgement within a few working days and an assessment after that.
Please give us a chance to ship a fix before disclosing publicly.

## Threat model

This is a computation library. It has **no dependencies outside the standard library**,
opens no network connections, spawns no processes, and executes no user-supplied code except
the extension values you register yourself. The `packvium` command reads one request from the
input path it is given (or standard input) and writes the result to standard output or to the
file named with `-o`; it touches no other file.

The realistic risks are therefore about untrusted **input**:

- **Resource exhaustion.** A request with a large item count, many container types or a
  permissive solver profile can consume substantial CPU and memory. If you accept requests
  from untrusted callers, set `time_limit_ms` and an `effort_budget`, cap item quantities and
  container inventory, and run the call where you can bound memory. The deadline is honoured
  by the search, but a single enormous request can still allocate a lot before the first
  check.
- **Integer magnitude.** Dimensions and weights become exact 64-bit integer ticks (16 000 per
  millimetre). A value too large for 64 bits is refused rather than allowed to overflow, but
  large values within range still cost time and memory. Validate dimensions against a sane
  maximum before passing them in.
- **Malformed input.** Bad units, unparseable numbers and contradictory constraints return
  errors rather than producing a wrong packing. Do not ignore those errors.

A result that reports success has passed independent validation, but validation checks the
constraints you declared. It cannot know about a constraint you did not express.

## Extensions

Custom constraints, orderings, scorers, container selectors and solvers run with the
privileges of your process. Treat a third-party extension as you would any other dependency:
read it before you register it.

## How releases are published

A Go module has no upload step. A release is a git tag in this repository. The Go module
proxy (`proxy.golang.org`) fetches the module from that tag the first time anyone asks for
the version, and the Go checksum database (`sum.golang.org`) records its hash. From then on,
`go` refuses any copy whose contents differ from that record. There is no upload token to
protect.

Release tags are plain lightweight tags; they are not signed. What protects a version once it
is published is the checksum database, not a signature.

A defective or compromised version is marked `retract` in `go.mod` in a fixed release, so that
`go get` steers away from it. The security advisory names the affected versions.
