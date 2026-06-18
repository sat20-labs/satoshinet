package blockchain

import (
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func contractValidationPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contract.TestnetContractPrefix
	}
	if params.Net == wire.MainNet {
		return contract.MainnetContractPrefix
	}
	return contract.TestnetContractPrefix
}
