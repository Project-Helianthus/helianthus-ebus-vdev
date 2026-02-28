package device

import "github.com/Project-Helianthus/helianthus-ebusgo/emulation"

// Device represents a virtual eBUS device that can be emulated on the bus.
type Device interface {
	// Name returns a human-readable device name (e.g., "VR90-Zone3").
	Name() string

	// Target builds an emulation Target configured for the given slave address.
	Target(address byte) (*emulation.Target, error)

	// CandidateAddresses returns the allowed master addresses for GentleJoin.
	// An empty slice means any initiator address is acceptable.
	CandidateAddresses() []byte
}
