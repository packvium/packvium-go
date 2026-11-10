// Package nested packs level by level: what one level packs becomes the items of the next,
// so units go into cartons and cartons go onto pallets in one call.
package nested

import (
	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/result"
)

// Level is one tier of the nesting: its name, the containers offered at it, and the
// configuration it is solved with (nil for the balanced defaults).
type Level struct {
	Name       string
	Containers []*domain.Container
	Config     *config.PackingConfig
}

// Result holds one packing result per level that was attempted.
type Result struct {
	Levels []*result.PackingResult
}

// Pack solves each level in turn. It stops after the first level that leaves an item
// behind, because the next level would otherwise ship an incomplete load as if it were
// complete. O(levels) packing calls.
func Pack(items []*domain.Item, levels []Level) (*Result, error) {
	current := append([]*domain.Item(nil), items...)
	outcome := &Result{}
	for _, level := range levels {
		cfg := level.Config
		if cfg == nil {
			balanced, err := config.New(config.Default(config.Balanced))
			if err != nil {
				return nil, err
			}
			cfg = balanced
		}
		packed, err := packer.New(cfg, algorithm.NoExtensions()).Pack(current, level.Containers)
		if err != nil {
			return nil, err
		}
		outcome.Levels = append(outcome.Levels, packed)
		if !packed.Complete() {
			break
		}
		next := make([]*domain.Item, 0, len(packed.Containers))
		for _, container := range packed.Containers {
			// A packed container travels upright, with no load limit of its own.
			item, err := container.AsItem("", true, nil)
			if err != nil {
				return nil, err
			}
			next = append(next, item)
		}
		current = next
	}
	return outcome, nil
}
