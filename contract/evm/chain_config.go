package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/params"
)

const EVMRulesVersion uint32 = 1

// newSatoshiNetChainConfigV1 fixes the consensus-visible EVM feature set to
// Paris. Dependency upgrades must not silently activate later Ethereum forks.
func newSatoshiNetChainConfigV1() *params.ChainConfig {
	zero := func() *big.Int { return new(big.Int) }
	return &params.ChainConfig{
		ChainID:                 big.NewInt(1337),
		HomesteadBlock:          zero(),
		EIP150Block:             zero(),
		EIP155Block:             zero(),
		EIP158Block:             zero(),
		ByzantiumBlock:          zero(),
		ConstantinopleBlock:     zero(),
		PetersburgBlock:         zero(),
		IstanbulBlock:           zero(),
		MuirGlacierBlock:        zero(),
		BerlinBlock:             zero(),
		LondonBlock:             zero(),
		ArrowGlacierBlock:       zero(),
		GrayGlacierBlock:        zero(),
		TerminalTotalDifficulty: zero(),
		ShanghaiTime:            nil,
		CancunTime:              nil,
		PragueTime:              nil,
		OsakaTime:               nil,
		AmsterdamTime:           nil,
		VerkleTime:              nil,
	}
}
