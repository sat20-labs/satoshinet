package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/stretchr/testify/require"
)

func TestSatoshiNetZeroBlockSubsidy(t *testing.T) {
	for _, params := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams, &chaincfg.RegressionNetParams, &chaincfg.SimNetParams} {
		for _, height := range []int32{0, 1, 210000, 420000, 2147483647} {
			require.Zero(t, CalcBlockSubsidy(height, params), "%s height %d", params.Name, height)
		}
	}
}

func TestCoinbaseCannotMintBTCWithoutFees(t *testing.T) {
	for _, value := range []int64{0, 1} {
		chain, _, _, teardown := setupReadinessChain(t)
		candidate := readinessTestBlock(t, chain).MsgBlock().Copy()
		candidate.Transactions[0].TxOut[0].Value = value
		var txs []*btcutil.Tx
		for _, tx := range candidate.Transactions {
			txs = append(txs, btcutil.NewTx(tx))
		}
		candidate.Header.MerkleRoot = CalcMerkleRoot(txs, false)
		err := chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate))
		if value == 0 {
			require.NoError(t, err)
		} else {
			var ruleErr RuleError
			require.ErrorAs(t, err, &ruleErr)
			require.Equal(t, ErrBadCoinbaseValue, ruleErr.ErrorCode)
		}
		teardown()
	}
}
