// Package packvium is the Go port of the Packvium deterministic 3D bin-packing library.
//
// The port replays the reference engine's release history milestone by milestone; see
// PORTING-STATUS.md for which milestone this tree implements. Layers follow the
// reference architecture and depend in one direction only:
//
//	units -> domain -> constraints/algorithms/validation -> packer -> serialization
package packvium
