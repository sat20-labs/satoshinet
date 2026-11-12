package chaincfg

import "github.com/sat20-labs/satoshinet/wire"

// POSV2Active deliberately excludes genesis and unscheduled networks.
func (p *Params) POSV2Active(height int32) bool {
	return p != nil && p.POSV2Height > 0 && height >= p.POSV2Height
}

// ContractsActive gates native execution on mainnet and preserves the existing
// contract rules on networks where execution predates POS v2.
func (p *Params) ContractsActive(height int32) bool {
	return p != nil && (p.Net != wire.MainNet || p.POSV2Active(height))
}
