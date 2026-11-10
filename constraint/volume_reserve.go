package constraint

import (
	"math/big"

	"github.com/toxakara/packvium-go/domain"
)

// ReservedVolume is the volume set aside for packing material, exact even for a metre-scale
// container whose volume a float64 cannot carry: this is a feasibility number.
func ReservedVolume(container *domain.Container) *big.Int {
	volume := container.InnerDimensions().Volume()
	volume.Mul(volume, big.NewInt(ScaledRatio(container.VoidFillReserveRatio())))
	return volume.Quo(volume, big.NewInt(SupportScale))
}

// UsableVolume is the inner volume less the reserve.
func UsableVolume(container *domain.Container) *big.Int {
	volume := container.InnerDimensions().Volume()
	return volume.Sub(volume, ReservedVolume(container))
}
