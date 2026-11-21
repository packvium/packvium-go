package domain

import (
	"errors"
	"fmt"
)

// FixedPlacement is an item already in a known place before the solve: loaded, or locked
// there. It is addressed by container type and instance rather than by a result's container
// index, which does not exist until the solve finishes. Position is the physical origin, as a
// result reports it, so a result placement can be fixed by quoting it
// (docs/PLAN-REVISIONS.md).
type FixedPlacement struct {
	itemID            string
	containerID       string
	position          Point
	rotation          Rotation
	containerInstance int64
}

// NewFixedPlacement refuses an instance below one: instances count from 1.
func NewFixedPlacement(itemID, containerID string, position Point, rotation Rotation,
	containerInstance int64) (FixedPlacement, error) {
	if containerInstance < 1 {
		return FixedPlacement{}, errors.New("container_instance counts from 1")
	}
	return FixedPlacement{itemID, containerID, position, rotation, containerInstance}, nil
}

func (f FixedPlacement) ItemID() string           { return f.itemID }
func (f FixedPlacement) ContainerID() string      { return f.containerID }
func (f FixedPlacement) Position() Point          { return f.position }
func (f FixedPlacement) Rotation() Rotation       { return f.rotation }
func (f FixedPlacement) ContainerInstance() int64 { return f.containerInstance }

// PackedContainerID is the id the packed container holding it reports, e.g. "box#1".
func (f FixedPlacement) PackedContainerID() string {
	return fmt.Sprintf("%s#%d", f.containerID, f.containerInstance)
}
