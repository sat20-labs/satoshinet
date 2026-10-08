package blockchain

import (
	"encoding/asn1"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"math/big"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Exercise the consensus input validator at the activation height. Mutations
// preserve the signed funding, signature, total value and total assets.
func TestPOSV2AnchorRejectsUnsignedOutputChanges(t *testing.T) {
	for _, mutation := range []string{"recipient", "value-distribution", "asset-distribution"} {
		t.Run(mutation, func(t *testing.T) {
			chain, _, key, _, _, teardown := setupPOSChain(t)
			defer teardown()
			assets := wire.TxAssets{{Name: *wire.NewAssetNameFromString("brc20:f:authorization"), Amount: *indexercommon.NewDecimal(100, 2)}}
			anchor, _ := replayTestAnchor(t, assets)
			address, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(key.PubKey().SerializeCompressed()), chain.chainParams)
			require.NoError(t, err)
			recipient, err := txscript.PayToAddrScript(address)
			require.NoError(t, err)
			anchor.TxOut[0].Value = 20000
			anchor.AddTxOut(wire.NewTxOut(1000, nil, recipient))
			signReplayAnchorOutputs(t, anchor, true)
			_, _, err = CheckTransactionInputs(btcutil.NewTx(anchor), false, chain.chainParams.POSV2Height, NewUtxoViewpoint(), chain.chainParams)
			require.NoError(t, err)

			modified := anchor.Copy()
			switch mutation {
			case "recipient":
				modified.TxOut[0].PkScript = recipient
			case "value-distribution":
				modified.TxOut[0].Value--
				modified.TxOut[2].Value++
			case "asset-distribution":
				modified.TxOut[0].Assets = assets.Clone()
				modified.TxOut[0].Assets[0].Amount = *indexercommon.NewDecimal(60, 2)
				modified.TxOut[2].Assets = assets.Clone()
				modified.TxOut[2].Assets[0].Amount = *indexercommon.NewDecimal(40, 2)
			}
			require.Equal(t, anchor.TxIn[0].SignatureScript, modified.TxIn[0].SignatureScript)
			_, _, err = CheckTransactionInputs(btcutil.NewTx(modified), false, chain.chainParams.POSV2Height, NewUtxoViewpoint(), chain.chainParams)
			require.Error(t, err, "the invoice must authorize the actual outputs")
		})
	}
}

func TestAnchorInvoiceActivationBoundary(t *testing.T) {
	anchor, _ := replayTestAnchor(t)
	params := chaincfg.TestNetParams
	params.POSV2Height = 2
	_, _, err := CheckTransactionInputs(btcutil.NewTx(anchor), false, 1, NewUtxoViewpoint(), &params)
	require.NoError(t, err, "legacy invoice must remain replayable below activation")
	legacyEncoding := anchor.Copy()
	legacyEncoding.LockTime = 1
	legacyEncoding.TxIn[0].SignatureScript = append(legacyEncoding.TxIn[0].SignatureScript, txscript.OP_0)
	_, _, err = CheckTransactionInputs(btcutil.NewTx(legacyEncoding), false, 1, NewUtxoViewpoint(), &params)
	require.NoError(t, err, "the activated encoding constraint must not change historical replay")

	_, _, err = CheckTransactionInputs(btcutil.NewTx(anchor), false, 2, NewUtxoViewpoint(), &params)
	require.Error(t, err, "legacy signatures must not authorize activated outputs")
	signReplayAnchorOutputs(t, anchor, true)
	_, _, err = CheckTransactionInputs(btcutil.NewTx(anchor), false, 2, NewUtxoViewpoint(), &params)
	require.NoError(t, err)
	_, _, err = CheckTransactionInputs(btcutil.NewTx(anchor), false, 1, NewUtxoViewpoint(), &params)
	require.Error(t, err, "replay selects the historical rule by block height")
}

func TestPOSV2AnchorTemplateAndBlockRejectChangedPayee(t *testing.T) {
	chain, _, key, _, candidate, teardown := setupPOSChain(t)
	defer teardown()
	chain.SetTipHeight(100)
	anchor, _ := replayTestAnchor(t)
	signReplayAnchorOutputs(t, anchor, true)
	candidate.AddTransaction(anchor)
	updatePOSCommitments(candidate)
	require.NoError(t, chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy())))
	recipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(key.PubKey().SerializeCompressed()), chain.chainParams)
	require.NoError(t, err)
	anchor.TxOut[0].PkScript, err = txscript.PayToAddrScript(recipient)
	require.NoError(t, err)
	updatePOSCommitments(candidate)
	require.ErrorContains(t, chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy())), "VerifyMessage failed")
	signature, err := signPOS(key)(common.POSApprovalMessage(chain.chainParams.Net, 1, candidate.BlockHash()))
	require.NoError(t, err)
	candidate.Transactions[0].TxIn[0].Witness = append(candidate.Transactions[0].TxIn[0].Witness, signature)
	main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(candidate), BFNone)
	require.ErrorContains(t, err, "VerifyMessage failed")
	require.False(t, main)
	require.False(t, orphan)
	require.Equal(t, int32(0), chain.BestSnapshot().Height)
}

// Every variant retains a valid authorization over the same outputs. Enforcing
// the builder's encoding, rather than finality alone, protects presigned outpoints.
func TestPOSV2AnchorRejectsTxIDMalleability(t *testing.T) {
	anchor, _ := replayTestAnchor(t)
	anchor.TxIn[0].Sequence = wire.AnchorTxOutIndex
	signReplayAnchorOutputs(t, anchor, true)
	_, err := anchortx.CheckAnchorTxValid(anchor, false, true)
	require.NoError(t, err)
	info, err := anchortx.ParseAnchorScript(anchor.TxIn[0].SignatureScript)
	require.NoError(t, err)
	for _, mutation := range []string{"final-locktime", "future-locktime", "version", "sequence", "prev-hash", "prev-index", "trailing-opcode", "trailing-invalid-push", "signature-push", "amount-push", "oversized-amount", "high-s", "signature-trailing-byte"} {
		t.Run(mutation, func(t *testing.T) {
			tx := anchor.Copy()
			switch mutation {
			case "final-locktime":
				tx.LockTime = 1
				require.True(t, IsFinalizedTransaction(btcutil.NewTx(tx), 100, time.Unix(0, 0)))
			case "future-locktime":
				tx.LockTime = 500
			case "version":
				tx.Version++
			case "sequence":
				tx.TxIn[0].Sequence++
			case "prev-hash":
				tx.TxIn[0].PreviousOutPoint.Hash[0] = 1
			case "prev-index":
				tx.TxIn[0].PreviousOutPoint.Index--
			case "trailing-opcode":
				tx.TxIn[0].SignatureScript = append(tx.TxIn[0].SignatureScript, txscript.OP_0)
			case "trailing-invalid-push":
				tx.TxIn[0].SignatureScript = append(tx.TxIn[0].SignatureScript, txscript.OP_PUSHDATA1)
			case "signature-push":
				script := tx.TxIn[0].SignatureScript
				offset := len(script) - len(info.Sig) - 1
				tx.TxIn[0].SignatureScript = append(append(append([]byte{}, script[:offset]...), txscript.OP_PUSHDATA1, byte(len(info.Sig))), info.Sig...)
			case "amount-push", "oversized-amount":
				tokenizer := txscript.MakeScriptTokenizer(0, tx.TxIn[0].SignatureScript)
				require.True(t, tokenizer.Next())
				require.True(t, tokenizer.Next())
				start := int(tokenizer.ByteIndex())
				require.True(t, tokenizer.Next())
				end := int(tokenizer.ByteIndex())
				data := append(append([]byte{}, tokenizer.Data()...), 0)
				if mutation == "oversized-amount" {
					for len(data) < 9 {
						data = append(data, 0)
					}
					data[len(data)-1] = 0x80
				}
				script := append(append([]byte{}, tx.TxIn[0].SignatureScript[:start]...), byte(len(data)))
				script = append(script, data...)
				tx.TxIn[0].SignatureScript = append(script, tx.TxIn[0].SignatureScript[end:]...)
			case "high-s", "signature-trailing-byte":
				sigBytes := append([]byte{}, info.Sig...)
				if mutation == "high-s" {
					var der struct{ R, S *big.Int }
					_, err := asn1.Unmarshal(sigBytes, &der)
					require.NoError(t, err)
					der.S.Sub(btcec.S256().Params().N, der.S)
					sigBytes, err = asn1.Marshal(der)
					require.NoError(t, err)
					sig, err := ecdsa.ParseDERSignature(sigBytes)
					require.NoError(t, err)
					invoice, err := common.AnchorInvoice(tx, true)
					require.NoError(t, err)
					require.True(t, sig.Verify(chainhash.HashB(invoice), replayAnchorCoreKey(t).PubKey()))
				} else {
					sigBytes = append(sigBytes, 0)
				}
				tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(info.Utxo, info.WitnessScript, info.Value, info.TxAssets, sigBytes)
				require.NoError(t, err)
			}
			require.NotEqual(t, anchor.TxHash(), tx.TxHash())
			var err error
			require.NotPanics(t, func() { _, err = anchortx.CheckAnchorTxValid(tx, false, true) })
			require.Error(t, err, "an unchanged invoice must not authorize a different txid")
			_, err = anchortx.GetLockedTxInfo(tx, false, true)
			require.Error(t, err, "template and connection paths must enforce the same encoding")
			params := chaincfg.TestNetParams
			params.POSV2Height = 2
			_, _, err = CheckTransactionInputs(btcutil.NewTx(tx), false, 100, NewUtxoViewpoint(), &params)
			require.Error(t, err, "formal consensus input validation must reject the variant")

		})
	}
}
