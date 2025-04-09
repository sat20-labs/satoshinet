package utils

import (
	"strconv"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

// Get current generator info in record this peer
func GetValidatorPort(ChainParams *chaincfg.Params) int {

	port, err := strconv.Atoi(ChainParams.DefaultPort)
	if err != nil {
		Log.Errorf("GetValidatorPort failed: %v", err)
		return 9525
	}
	port -= 1

	return port
}
