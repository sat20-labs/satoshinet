package node

import (
	"fmt"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestCombinedRootActivationWithoutActivity(t *testing.T) {
	params := chaincfg.TestNetParams
	params.POSV2Height = 10
	roots := [3][32]byte{testHashRoot(1), testHashRoot(2), testHashRoot(3)}
	for _, empty := range []bool{false, true} {
		cfg := CompositeContractBlockValidatorConfig{ChainParams: &params}
		if !empty {
			cfg.TemplateValidator = testRootValidator{parentRoot: roots[0]}
			cfg.EVMValidator = testRootValidator{parentRoot: roots[1]}
			cfg.AgentValidator = testRootValidator{parentRoot: roots[2]}
		}
		want := contract.CombineStateRoots(roots[0], roots[1], roots[2])
		if empty {
			want = contract.CombineStateRoots([32]byte{}, [32]byte{}, [32]byte{})
		}
		for _, tc := range []struct {
			name   string
			height int32
			kind   string
			valid  bool
		}{
			{"before", 9, "missing", true}, {"genesis", 0, "missing", true},
			{"missing", 10, "missing", false}, {"wrong", 10, "wrong", false},
			{"duplicate", 10, "duplicate", false}, {"correct", 10, "correct", true},
			{"after", 11, "correct", true},
		} {
			t.Run(tc.name+map[bool]string{false: "/parents", true: "/empty"}[empty], func(t *testing.T) {
				coinbase := testEVMCoinbaseTx()
				root := want
				if tc.kind == "wrong" {
					root[0] ^= 1
				}
				if tc.kind != "missing" {
					require.NoError(t, contract.UpsertCoinbaseStateRoot(coinbase, root))
				}
				if tc.kind == "duplicate" {
					out := *coinbase.TxOut[len(coinbase.TxOut)-1]
					coinbase.AddTxOut(&out)
				}
				block := btcutil.NewBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{coinbase}})
				block.SetHeight(tc.height)
				err := NewCompositeContractBlockValidator(cfg).ValidateContractBlock(block, blockchain.NewUtxoViewpoint())
				if tc.valid {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "state root")
				}
			})
		}
	}
}

func TestCombinedRootCommitsDueAgentWithoutTransactions(t *testing.T) {
	store := testReadyAgentRuntimeStore(t, agent.TimeBaseHeight, 10, 30)
	executed, err := agent.ExecuteBlock(agent.BlockExecutionRequest{
		Store: store.Clone(), BlockHeight: 11, BlockTime: time.Unix(1710000000, 0).Unix(),
	})
	require.NoError(t, err)
	require.NotEqual(t, store.StateRoot(), executed.StateRoot)
	params := chaincfg.TestNetParams
	params.POSV2Height = 10
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			root := store.StateRoot()
			if current {
				root = executed.StateRoot
			}
			coinbase := testEVMCoinbaseTx()
			require.NoError(t, contract.UpsertCoinbaseStateRoot(coinbase, contract.CombineStateRoots([32]byte{}, [32]byte{}, root)))
			block := btcutil.NewBlock(&wire.MsgBlock{Header: wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)}, Transactions: []*wire.MsgTx{coinbase}})
			block.SetHeight(11)
			validator := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
				ChainParams: &params,
				AgentValidator: NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
					ChainParams: &params,
					NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
						return store.Clone(), nil
					},
				}),
			})
			err := validator.ValidateContractBlock(block, blockchain.NewUtxoViewpoint())
			if current {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "state root mismatch")
			}
		})
	}
}
