package validatorpeer

import (
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/validatorcommand"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	// MaxValidatorVersion is the max validator version the peer supports.
	MaxValidatorVersion = validatorcommand.VALIDATOR_VERION

	// DefaultTrickleInterval is the min time between attempts to send an
	// inv message to a peer.
	DefaultTrickleInterval = 10 * time.Second
)

// HostToNetAddrFunc is a func which takes a host, port, services and returns
// the netaddress.
type HostToNetAddrFunc func(host string, port uint16,
	services wire.ServiceFlag) (*wire.NetAddressV2, error)
