package evm

import (
	"math/big"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

// Compare the executed bytecode and selector on the direct and transaction
// paths. Trace output is emitted only for a failed test, never by a node.
func TestManagedCloseTransactionTrace(t *testing.T) {
	const asset = "ordx:ft:profit"
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	for _, path := range []string{"direct", "transaction"} {
		t.Run(path, func(t *testing.T) {
			runtime := NewRuntime(nil)
			runtime.SetCode(ContractAddressHash(addr), evmCloseTransferCode(asset, "tb1qdest", "40"))
			runtime.State.SetContractDeployer(ContractGethAddress(addr), caller.String())
			seedEVMManagedFixture(t, runtime, addr, 0, map[string]string{asset: "100"})
			runtime.Config.Tracer = &tracing.Hooks{
				OnEnter: func(depth int, typ byte, from, to gethcommon.Address, input []byte, gas uint64, value *big.Int) {
					t.Logf("enter depth=%d type=%x from=%s to=%s input=%x gas=%d", depth, typ, from, to, input, gas)
				},
				OnExit: func(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
					t.Logf("exit depth=%d output=%x used=%d err=%v reverted=%t", depth, output, gasUsed, err, reverted)
				},
			}
			cfg := GasConfig{GasAssetName: DefaultGasConfig().GasAssetName, BootstrapAddress: "bootstrap",
				FixedGasPrice: 1, ResultBaseGas: 5, InvokeBaseGas: contract.InvokeBaseGas,
				DeployBaseGas: contract.DeployBaseGas, MaxGasPerBlock: contract.MaxGasPerBlock}
			backend := NewBackend(BlockExecutionRequest{Runtime: runtime, GasConfig: cfg, Block: testBlockContext(100),
				ResolveCaller: fixedCaller(caller), ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
				ContractUTXOs: func(ContractAddress) ([]UTXO, error) {
					return []UTXO{mustUTXO(t, OutPoint{TxID: chainhash.Hash{9}.String()}, addr, asset, 100, 0)}, nil
				},
				AssetPrecision: func(name string) (int, bool) { return 0, name == asset || name == cfg.GasAssetName },
				ResolveResultScript: evmTestResultScriptResolver(t, addr),
			})
			if path == "direct" {
				result := runtime.Call(CallRequest{CallerAddress: caller.String(), TargetAddress: addr.MustEncode(),
					Input: evmCloseHookCalldata(), Gas: contract.InvokeBaseGas, Block: testBlockContext(100)})
				require.NoError(t, result.Err)
				require.True(t, evmCloseHookSucceeded(result))
				require.Len(t, runtime.AssetIntents, 1)
				return
			}
			tx := blockResultInvokeTx(t, addr, InvokePayload{GasLimit: contract.InvokeBaseGas, Action: contract.ContractInvokeAPIClose}, cfg.GasAssetName, 6000)
			require.NoError(t, backend.ExecuteTx(tx))
			require.Len(t, backend.records, 1)
			require.Equal(t, ResultStatusSuccess, backend.records[0].Status)
			require.Len(t, runtime.AssetIntents, 1)
		})
	}
}
