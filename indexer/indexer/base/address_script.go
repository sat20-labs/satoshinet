package base

import (
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func addressToPkScript(address string, isMainnet bool) ([]byte, error) {
	chain := common.ChainTestnet
	if isMainnet {
		chain = common.ChainMainnet
	}
	return common.AddrToPkScript(address, chain)
}
