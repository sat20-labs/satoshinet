package blockchain

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain/internal/testhelper"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func resultWorkFixture(t testing.TB, count int) (*wire.MsgTx, *UtxoViewpoint) {
	t.Helper()
	address, err := contract.NewContractAddress(contract.TestnetContractPrefix,
		contract.AddressVersionV1, contract.ContractTypeTemplate, contract.EVMAddress{1})
	require.NoError(t, err)
	script, err := contract.ContractPkScript(address)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	view := NewUtxoViewpoint()
	for i := 0; i < count; i++ {
		point := wire.OutPoint{Hash: chainhash.Hash{1}, Index: uint32(i)}
		tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
		view.entries[point] = NewUtxoEntry(wire.NewTxOut(1, nil, script), 1, false)
		tx.AddTxOut(wire.NewTxOut(1, nil, []byte{txscript.OP_TRUE}))
	}
	resultScript, err := contract.ResultNullDataScript(contract.ResultPayload{
		Status: contract.ResultStatusSuccess, ResultCount: uint16(count),
	})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	return tx, view
}

// Exercise both public callers of txValidator, including errors beyond the
// representative contract input and ordinary inputs in the same transaction.
func TestResultValidationWorkRejections(t *testing.T) {
	for _, blockEntry := range []bool{false, true} {
		for _, name := range []string{"valid", "late_mixed_module", "late_invalid_script", "missing_input", "non_result", "zero_count", "duplicate_payload"} {
			t.Run(fmt.Sprintf("block=%v/%s", blockEntry, name), func(t *testing.T) {
				tx, view := resultWorkFixture(t, 8)
				last := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
				switch name {
				case "late_mixed_module":
					view.entries[last] = NewUtxoEntry(testEVMContractTxOut(t), 1, false)
				case "late_invalid_script":
					view.entries[last].pkScript = []byte{txscript.OP_FALSE}
				case "missing_input":
					delete(view.entries, last)
				case "non_result":
					tx.TxOut = tx.TxOut[:len(tx.TxOut)-1]
				case "zero_count":
					script, err := contract.ResultNullDataScript(contract.ResultPayload{ResultCount: 0})
					require.NoError(t, err)
					tx.TxOut[len(tx.TxOut)-1].PkScript = script
				case "duplicate_payload":
					tx.AddTxOut(tx.TxOut[len(tx.TxOut)-1])
				}
				err := resultWorkValidate(btcutil.NewTx(tx), view, 0, blockEntry)
				if name == "valid" {
					require.NoError(t, err)
					t.Log("decision=accept")
					return
				}
				require.Error(t, err)
				rule, ok := err.(RuleError)
				require.True(t, ok, "%T: %v", err, err)
				if name == "missing_input" {
					// Existing concurrent workers may report either the missing
					// input itself or the full contract check's missing script.
					require.Contains(t, []ErrorCode{ErrMissingTxOut, ErrScriptValidation}, rule.ErrorCode)
				} else {
					require.Equal(t, ErrScriptValidation, rule.ErrorCode)
				}
				t.Logf("decision=reject code=%s", rule.ErrorCode)
			})
		}
	}
}

func resultWorkValidate(tx *btcutil.Tx, view *UtxoViewpoint, flags txscript.ScriptFlags, blockEntry bool) error {
	cache := txscript.NewHashCache(10)
	if blockEntry {
		return checkBlockScripts(btcutil.NewBlock(&wire.MsgBlock{
			Transactions: []*wire.MsgTx{tx.MsgTx()},
		}), view, flags, nil, cache, &chaincfg.TestNetParams)
	}
	return ValidateTransactionScripts(tx, view, flags, nil, cache)
}

func TestResultValidationWorkUsesCurrentWitnessAndUTXO(t *testing.T) {
	for _, blockEntry := range []bool{false, true} {
		t.Run(fmt.Sprintf("block=%v", blockEntry), func(t *testing.T) {
			tx, view := resultWorkFixture(t, 8)
			key, pub := btcec.PrivKeyFromBytes([]byte{7})
			pubHash := btcutil.Hash160(pub.SerializeCompressed())
			address, err := btcutil.NewAddressWitnessPubKeyHash(pubHash, &chaincfg.TestNetParams)
			require.NoError(t, err)
			script, err := txscript.PayToAddrScript(address)
			require.NoError(t, err)
			point := wire.OutPoint{Hash: chainhash.Hash{2}}
			tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
			view.entries[point] = NewUtxoEntry(wire.NewTxOut(1000, nil, script), 1, false)
			pkh, err := btcutil.NewAddressPubKeyHash(pubHash, &chaincfg.TestNetParams)
			require.NoError(t, err)
			scriptCode, err := txscript.PayToAddrScript(pkh)
			require.NoError(t, err)
			ordinary := len(tx.TxIn) - 1
			hashes := txscript.NewTxSigHashes(tx, view)
			tx.TxIn[ordinary].Witness, err = txscript.WitnessSignature(tx, hashes, ordinary,
				1000, nil, scriptCode, txscript.SigHashAll, key, true)
			require.NoError(t, err)
			wrapped := btcutil.NewTx(tx)
			flags := txscript.ScriptBip16 | txscript.ScriptVerifyWitness
			require.NoError(t, resultWorkValidate(wrapped, view, flags, blockEntry), "valid real signature")
			before := *wrapped.Hash()
			tx.TxIn[ordinary].Witness[0][10] ^= 1
			require.Equal(t, before, tx.TxHash(), "witness mutation preserves txid")
			require.Error(t, resultWorkValidate(wrapped, view, flags, blockEntry), "ordinary signature must be checked again")
			tx.TxIn[ordinary].Witness[0][10] ^= 1
			require.NoError(t, resultWorkValidate(wrapped, view, flags, blockEntry))
			view.entries[tx.TxIn[7].PreviousOutPoint] = NewUtxoEntry(testEVMContractTxOut(t), 1, false)
			require.ErrorContains(t, resultWorkValidate(wrapped, view, flags, blockEntry), "multiple modules", "current view must be checked again")
			t.Log("decision=accept,reject-witness,accept,reject-current-utxo")
		})
	}
}

func TestResultValidationWorkRetainsFormalValidation(t *testing.T) {
	chain, _, validator, teardown := setupReadinessChain(t)
	defer teardown()
	// Current regtest activates BIP34/65/66 at height 1. The older readiness
	// fixture uses a version-1 header, so rebuild it with a valid header.
	msg := readinessTestBlock(t, chain).MsgBlock()
	msg.Header.Version = 4
	require.True(t, testhelper.SolveBlock(&msg.Header))
	block := btcutil.NewBlock(msg)
	block.SetHeight(1)
	require.NoError(t, chain.CheckConnectBlockTemplate(block))
	_, _, err := chain.ProcessBlock(block, BFNone)
	require.NoError(t, err)
	require.EqualValues(t, 2, validator.calls.Load(), "proposal and formal acceptance must both execute full validation")
	t.Log("validation_calls=2")
}

func TestResultValidationWorkChecksEachTransaction(t *testing.T) {
	first, view := resultWorkFixture(t, 4)
	second, otherView := resultWorkFixture(t, 4)
	for _, input := range second.TxIn {
		entry := otherView.LookupEntry(input.PreviousOutPoint)
		input.PreviousOutPoint.Hash = chainhash.Hash{3}
		view.entries[input.PreviousOutPoint] = entry
	}
	second.TxOut = second.TxOut[:len(second.TxOut)-1]
	block := btcutil.NewBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{first, second}})
	err := checkBlockScripts(block, view, 0, nil, nil, &chaincfg.TestNetParams)
	require.ErrorContains(t, err, "not a CONTRACT_RESULT", "validating one transaction must not authorize another")
	t.Log("decision=reject-invalid-second-result")
}

func BenchmarkResultScriptValidationWork(b *testing.B) {
	for _, count := range []int{128, 512, 1001} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			tx, view := resultWorkFixture(b, count)
			wrapped := btcutil.NewTx(tx)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := ValidateTransactionScripts(wrapped, view, 0, nil, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
