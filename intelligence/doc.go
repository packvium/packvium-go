// Package intelligence groups the decision layer that sits above the packer: deciding whether
// a change is an improvement before it is published. It holds no code of its own; the work is
// in five subpackages, each depending only on the ones before it:
//
//	pareto           dominance and a per-profile Pareto report, never a blended score
//	simulation       version-pinned scenarios over an order corpus, compared order by order
//	recommendations  a proposal from paired evidence, and the explicit approval that publishes it
//	outcomes         an append-only ledger of what actually happened to each shipment
//	holdout          replaying a recommendation against history it was not derived from
//
// None of them reads a clock or the network, and none packs anything itself: packing and
// validation reach them through caller-supplied functions, so the same inputs replay to the
// same answer. examples/intelligence walks through all five.
package intelligence
