package base

import (
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func addressToPkScript(address, chain string) ([]byte, error) {
	return common.AddrToPkScript(address, chain)
}
