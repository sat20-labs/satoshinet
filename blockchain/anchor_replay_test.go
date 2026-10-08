package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

// Use a signed Anchor and the real funding-UTXO database, so a parse or
// membership error cannot accidentally satisfy the rejection assertions.
func replayTestAnchor(t *testing.T, withAssets ...wire.TxAssets) (*wire.MsgTx, string) {
	t.Helper()
	oldTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = oldTesting })
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{
		ChainParams: &chaincfg.TestNetParams, IndexerScheme: "http",
		IndexerHost: "127.0.0.1:1", IndexerProxy: "testnet",
	}))
	t.Cleanup(anchortx.Stop)
	coreKey := replayAnchorCoreKey(t)
	peerKey, _ := btcec.PrivKeyFromBytes([]byte{2})
	witness, channelScript, err := anchortx.GetP2WSHscript(coreKey.PubKey().SerializeCompressed(), peerKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	funding := strings.Repeat("12", 32) + ":0"
	var txAssets wire.TxAssets
	if len(withAssets) != 0 {
		txAssets = withAssets[0]
	}
	invoice, err := anchortx.StandardAnchorScript(funding, witness, 21000, txAssets)
	require.NoError(t, err)
	assets, err := wire.SerializeTxAssets(&txAssets)
	require.NoError(t, err)
	sig := ecdsa.Sign(coreKey, chainhash.HashB(invoice)).Serialize()
	script, err := txscript.NewScriptBuilder().AddData([]byte(funding)).AddData(witness).
		AddInt64(21000).AddData(assets).AddData(sig).Script()
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: wire.AnchorTxOutIndex}, SignatureScript: script})
	tx.AddTxOut(wire.NewTxOut(21000, txAssets, channelScript))
	metadata := "::-2100000000000000-0-1"
	if len(txAssets) == 1 {
		a := txAssets[0]
		metadata = fmt.Sprintf("%s-1000000-%d-%d", a.Name.String(), a.Amount.Precision, a.BindingSat)
	}
	ascending, err := scommon.NullDataScript(scommon.CONTENT_TYPE_ASCENDING, []byte(metadata))
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, ascending))
	_, err = anchortx.GetLockedTxInfo(tx, false, false)
	require.NoError(t, err)
	return tx, funding
}

func replayAnchorCoreKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	seed := bip39.NewSeed("acquire pet news congress unveil erode paddle crumble blue fish match eye", "")
	key, err := hdkeychain.NewMaster(seed, &chaincfg.TestNetParams)
	require.NoError(t, err)
	for _, child := range []uint32{hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, 0, 0} {
		key, err = key.Derive(child)
		require.NoError(t, err)
	}
	coreKey, err := key.ECPrivKey()
	require.NoError(t, err)
	require.Equal(t, indexercommon.GetBootstrapPubKey(), hex.EncodeToString(coreKey.PubKey().SerializeCompressed()))

	return coreKey
}

func signReplayAnchorOutputs(t *testing.T, tx *wire.MsgTx, bindOutputs bool) {
	t.Helper()
	if bindOutputs {
		tx.Version = wire.TxVersion
		tx.TxIn[0].Sequence = wire.AnchorTxOutIndex
	}
	info, err := anchortx.ParseAnchorScript(tx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	invoice, err := scommon.AnchorInvoice(tx, bindOutputs)
	require.NoError(t, err)
	signature := ecdsa.Sign(replayAnchorCoreKey(t), chainhash.HashB(invoice)).Serialize()
	tx.TxIn[0].SignatureScript, err = scommon.StandardAnchorScriptWithSig(info.Utxo, info.WitnessScript, info.Value, info.TxAssets, signature)
	require.NoError(t, err)
}

func TestTestnet1748AnchorException(t *testing.T) {
	oldTesting, oldChain := indexercommon.ENABLE_TESTING, indexercommon.CHAIN
	indexercommon.ENABLE_TESTING, indexercommon.CHAIN = false, "testnet"
	t.Cleanup(func() { indexercommon.ENABLE_TESTING, indexercommon.CHAIN = oldTesting, oldChain })
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{
		ChainParams: &chaincfg.TestNetParams, IndexerScheme: "http",
		IndexerHost: "127.0.0.1:1", IndexerProxy: "testnet",
	}))
	t.Cleanup(anchortx.Stop)
	data, err := os.ReadFile("testdata/testnet-anchor-1748.hex")
	require.NoError(t, err)
	raw, err := hex.DecodeString(string(bytes.TrimSpace(data)))
	require.NoError(t, err)
	original, err := btcutil.NewBlockFromBytes(raw)
	require.NoError(t, err)
	require.Equal(t, "af898ea0b898e1e93024aef9e39aefd26077421666eabbac37f14f7743fa712e", original.Hash().String())
	var anchors []*wire.MsgTx
	for _, tx := range original.MsgBlock().Transactions {
		if tx.TxID() == "12818525d7a1a6801dce1143e03c2b4bea6f59c8e76c2e7dad1cb6f2fff094e7" ||
			tx.TxID() == "ffd55194f0dae8daf18b1831fda96257cd73ad835869838cc49c1cf1f6bcbd49" {
			anchors = append(anchors, tx)
		}
	}
	require.Len(t, anchors, 2)
	require.Equal(t, "12818525d7a1a6801dce1143e03c2b4bea6f59c8e76c2e7dad1cb6f2fff094e7", anchors[0].TxID())
	require.Equal(t, "ffd55194f0dae8daf18b1831fda96257cd73ad835869838cc49c1cf1f6bcbd49", anchors[1].TxID())
	locked, err := anchortx.GetLockedTxInfo(anchors[0], false, false)
	require.NoError(t, err)
	for _, name := range []string{"exact-block", "mainnet", "wrong-height", "different-block-hash", "unlisted-txid", "same-approved-tx-twice", "existing-approved-record", "existing-unapproved-record", "third-anchor-existing-record", "third-anchor-current-record", "third-anchor-unapproved-record", "third-anchor-altered-txid"} {
		t.Run(name, func(t *testing.T) {
			chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
			require.NoError(t, err)
			defer teardown()
			chain.chainParams.Net = wire.TestNet
			chain.SetTipHeight(3451)
			msg := original.MsgBlock().Copy()
			height := int32(1748)
			switch name {
			case "mainnet":
				chain.chainParams.Net = wire.MainNet
			case "wrong-height":
				height++
			case "different-block-hash":
				msg.Header.Nonce++
			case "unlisted-txid":
				// Even a caller presenting the approved header must not gain
				// an exception for transactions outside the approved pair.
				for _, tx := range msg.Transactions {
					if tx.TxID() == anchors[0].TxID() {
						tx.LockTime++
						break
					}
				}
			case "same-approved-tx-twice":
				for i, tx := range msg.Transactions {
					if tx.TxID() == anchors[1].TxID() {
						msg.Transactions[i] = anchors[0].Copy()
					}
				}
			case "existing-approved-record", "existing-unapproved-record":
				previous := anchors[1].Copy()
				if name == "existing-unapproved-record" {
					previous.LockTime++
				}
				require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
					return dbPutAnchorTxInfo(dbTx, &AnchorTxInfo{LockedUtxo: locked.Utxo,
						AnchorTxid: previous.TxID(), WitnessScript: locked.WitnessScript, Value: locked.Value})
				}))
			case "third-anchor-existing-record", "third-anchor-current-record", "third-anchor-unapproved-record", "third-anchor-altered-txid":
				for _, tx := range msg.Transactions {
					if tx.TxID() != "5da834f2e726daac5341dec4d7ce20c906969ac8517fb3b7f776b703b72030e9" {
						continue
					}
					third, err := anchortx.GetLockedTxInfo(tx, false, false)
					require.NoError(t, err)
					previous := "5387e75dbb0b585ee6aa43cef65fe55854485202ca2cd4b6c10fb36fea04d89e"
					if name == "third-anchor-current-record" {
						previous = tx.TxID()
					}
					if name == "third-anchor-unapproved-record" {
						previous = strings.Repeat("ab", 32)
					}
					if name == "third-anchor-altered-txid" {
						tx.LockTime++
					}
					require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
						return dbPutAnchorTxInfo(dbTx, &AnchorTxInfo{LockedUtxo: third.Utxo,
							AnchorTxid:    previous,
							WitnessScript: third.WitnessScript, Value: third.Value})
					}))
				}
			}
			block := btcutil.NewBlock(msg)
			block.SetHeight(height)
			err = chain.checkAnchorTxsUnique(block)
			if name == "exact-block" || name == "existing-approved-record" || name == "third-anchor-existing-record" || name == "third-anchor-current-record" {
				require.NoError(t, err)
				// This fixed historical block is also valid for offline replay
				// and branch revalidation, without a live sync peer.
				chain.SetTipHeight(0)
				require.NoError(t, chain.checkAnchorTxsUnique(block))
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "invalid anchor tx")
			}
		})
	}
}

func TestAnchorReplayUnique(t *testing.T) {
	tx, funding := replayTestAnchor(t)
	locked, err := anchortx.GetLockedTxInfo(tx, false, false)
	require.NoError(t, err)
	for _, tc := range []struct {
		name                 string
		net                  wire.BitcoinNet
		target               int
		height               int32
		different, duplicate bool
	}{
		{"same-testnet-history", wire.TestNet, 3, 1, false, false},
		{"same-testnet-final-sync-block", wire.TestNet, 3, 3, false, false},
		{"different-testnet-history", wire.TestNet, 3, 1, true, false},
		{"same-mainnet-history", wire.MainNet, 3, 1, false, false},
		{"same-testnet-live", wire.TestNet, 0, 1, false, false},
		{"same-testnet-beyond-target", wire.TestNet, 3, 4, false, false},
		{"duplicate-in-same-block", wire.TestNet, 3, 1, false, true},
		{"different-in-same-block", wire.TestNet, 3, 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
			require.NoError(t, err)
			defer teardown()
			chain.chainParams.Net = tc.net
			chain.SetTipHeight(tc.target)
			require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
				return dbPutAnchorTxInfo(dbTx, &AnchorTxInfo{LockedUtxo: funding, AnchorTxid: tx.TxID(),
					WitnessScript: locked.WitnessScript, Value: locked.Value})
			}))
			stored, err := chain.FetchAnchorTx(funding)
			require.NoError(t, err)
			require.Equal(t, tx.TxID(), stored.AnchorTxid)
			if tc.duplicate {
				// Isolate duplicate funding within the block from prior-chain reuse.
				require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
					return dbTx.Metadata().Bucket(anchorTxInfoBucketName).Delete([]byte(funding))
				}))
			}
			candidate := tx.Copy()
			if tc.different {
				candidate.TxOut[0].PkScript = []byte{txscript.OP_2}
			}
			msg := &wire.MsgBlock{Transactions: []*wire.MsgTx{candidate}}
			if tc.duplicate {
				msg.Transactions = []*wire.MsgTx{tx.Copy(), candidate.Copy()}
			}
			block := btcutil.NewBlock(msg)
			block.SetHeight(tc.height)
			err = chain.checkAnchorTxsUnique(block)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "invalid anchor tx")
			if tc.duplicate {
				require.ErrorContains(t, err, "block contains duplicate anchor funding utxo")
			}

		})
	}
}

func TestAnchorDuplicateFormalAcceptanceIndependentOfPeerHeight(t *testing.T) {
	tx, funding := replayTestAnchor(t)
	marker, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddOp(txscript.OP_16).AddInt64(1).AddData([]byte("::-21000000000000000-0-1")).Script()
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, marker))
	locked, err := anchortx.GetLockedTxInfo(tx, false, false)
	require.NoError(t, err)
	for _, target := range []int{0, 1, 1000000} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
			require.NoError(t, err)
			defer teardown()
			chain.chainParams.Net = wire.TestNet
			chain.SetTipHeight(target)
			require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
				return dbPutAnchorTxInfo(dbTx, &AnchorTxInfo{LockedUtxo: funding, AnchorTxid: tx.TxID(), WitnessScript: locked.WitnessScript, Value: locked.Value})
			}))
			msg := readinessTestBlock(t, chain).MsgBlock().Copy()
			msg.Transactions = append(msg.Transactions, tx.Copy())
			msg.Header.MerkleRoot = CalcMerkleRoot(btcutil.NewBlock(msg).Transactions(), false)
			main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(msg), BFNoPoWCheck)
			require.False(t, main)
			require.False(t, orphan)
			var rule RuleError
			require.ErrorAs(t, err, &rule)
			require.Equal(t, ErrAnchorTXVerifyFailed, rule.ErrorCode)
			require.ErrorContains(t, err, "already anchored")
		})
	}
}

func TestTestnet1708And1709AnchorExceptions(t *testing.T) {
	oldTesting, oldChain := indexercommon.ENABLE_TESTING, indexercommon.CHAIN
	indexercommon.ENABLE_TESTING, indexercommon.CHAIN = false, "testnet"
	t.Cleanup(func() { indexercommon.ENABLE_TESTING, indexercommon.CHAIN = oldTesting, oldChain })
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{ChainParams: &chaincfg.TestNetParams, IndexerScheme: "http", IndexerHost: "127.0.0.1:1", IndexerProxy: "testnet"}))
	t.Cleanup(anchortx.Stop)
	for _, height := range []int32{1708, 1709} {
		data, err := os.ReadFile(fmt.Sprintf("testdata/testnet-anchor-%d.hex", height))
		require.NoError(t, err)
		raw, err := hex.DecodeString(string(bytes.TrimSpace(data)))
		require.NoError(t, err)
		original, err := btcutil.NewBlockFromBytes(raw)
		require.NoError(t, err)
		tx := original.MsgBlock().Transactions[1]
		require.Equal(t, "2025513a5ad2bdb180bc1d239915fa813237f9c6724acfcaf5ae02971d803215", tx.TxID())
		locked, err := anchortx.GetLockedTxInfo(tx, false, false)
		require.NoError(t, err)
		for _, variant := range []string{"exact", "hash", "height", "txid", "mainnet", "previous-txid"} {
			t.Run(fmt.Sprintf("%d/%s", height, variant), func(t *testing.T) {
				chain, teardown, err := chainSetup(t.Name(), &chaincfg.RegressionNetParams)
				require.NoError(t, err)
				defer teardown()
				chain.chainParams.Net = wire.TestNet
				previous := tx.TxID()
				msg := original.MsgBlock().Copy()
				h := height
				switch variant {
				case "hash":
					msg.Header.Nonce++
				case "height":
					h++
				case "txid":
					msg.Transactions[1].LockTime++
				case "mainnet":
					chain.chainParams.Net = wire.MainNet
				case "previous-txid":
					previous = strings.Repeat("ab", 32)
				}
				require.NoError(t, chain.db.Update(func(dbTx database.Tx) error {
					return dbPutAnchorTxInfo(dbTx, &AnchorTxInfo{LockedUtxo: locked.Utxo, AnchorTxid: previous, WitnessScript: locked.WitnessScript, Value: locked.Value})
				}))
				for _, target := range []int{0, int(height), 1000000} {
					chain.SetTipHeight(target)
					block := btcutil.NewBlock(msg)
					block.SetHeight(h)
					err = chain.checkAnchorTxsUnique(block)
					if variant == "exact" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "already anchored")
					}
				}
			})
		}
	}
}

func TestAnchorAdmissionByTemplateAndFormalAcceptance(t *testing.T) {
	for _, mode := range []string{"deposit", "channel-and-dao", "wrong-value", "descending", "referrer", "precision", "binding-overflow"} {
		t.Run(mode, func(t *testing.T) {
			chain, _, key, _, candidate, teardown := setupPOSChain(t)
			defer teardown()
			chain.SetTipHeight(100) // Historical validation uses the signed invoice, without live L1 RPC.
			anchor, _ := replayTestAnchor(t)
			switch mode {
			case "deposit", "channel-and-dao":
				address, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(key.PubKey().SerializeCompressed()), &chaincfg.RegressionNetParams)
				require.NoError(t, err)
				script, err := txscript.PayToAddrScript(address)
				require.NoError(t, err)
				if mode == "deposit" {
					anchor.TxOut[0].PkScript = script
				} else {
					anchor.TxOut[0].Value = 20000
					anchor.AddTxOut(wire.NewTxOut(1000, nil, script))
				}
			case "wrong-value":
				anchor.TxOut[0].Value++
			case "descending", "referrer":
				operation := uint8(scommon.CONTENT_TYPE_DESCENDING)
				if mode == "referrer" {
					operation = scommon.CONTENT_TYPE_BINDREFERRER
				}
				script, err := scommon.NullDataScript(operation, []byte("payload"))
				require.NoError(t, err)
				anchor.AddTxOut(wire.NewTxOut(0, nil, script))
			case "precision", "binding-overflow":
				metadata := "::-2100000000000000-1-1"
				if mode == "binding-overflow" {
					metadata = "::-2100000000000000-0-4294967297"
				}
				script, err := scommon.NullDataScript(scommon.CONTENT_TYPE_ASCENDING, []byte(metadata))
				require.NoError(t, err)
				anchor.TxOut[1].PkScript = script
			}
			signReplayAnchorOutputs(t, anchor, true)
			candidate.AddTransaction(anchor)
			updatePOSCommitments(candidate)
			err := chain.CheckConnectBlockTemplate(btcutil.NewBlock(candidate.Copy()))
			valid := mode == "deposit" || mode == "channel-and-dao"
			if valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "invalid anchor tx")
			}
			// A distinct header exercises formal validation independently of the proposal cache.
			candidate.Header.Nonce++
			signature, err := signPOS(key)(scommon.POSApprovalMessage(chain.chainParams.Net, 1, candidate.BlockHash()))
			require.NoError(t, err)
			candidate.Transactions[0].TxIn[0].Witness = append(candidate.Transactions[0].TxIn[0].Witness, signature)
			main, orphan, err := chain.ProcessBlock(btcutil.NewBlock(candidate), BFNone)
			if valid {
				require.NoError(t, err)
				require.True(t, main)
				require.Equal(t, int32(1), chain.BestSnapshot().Height)
			} else {
				require.ErrorContains(t, err, "invalid anchor tx")
				require.False(t, main)
				require.Equal(t, int32(0), chain.BestSnapshot().Height)
			}
			require.False(t, orphan)
		})
	}
}

func TestLegalAnchorSplitOutputsCanBeSpentTogether(t *testing.T) {
	assets := wire.TxAssets{{Name: *wire.NewAssetNameFromString("ordx:f:split"), Amount: *indexercommon.NewDefaultDecimal(100), BindingSat: 1}}
	anchor, _ := replayTestAnchor(t, assets)
	script := anchor.TxOut[0].PkScript
	half := assets.Clone()
	half[0].Amount = *indexercommon.NewDefaultDecimal(50)
	anchor.TxOut[0] = wire.NewTxOut(10500, half.Clone(), script)
	anchor.AddTxOut(wire.NewTxOut(10500, half.Clone(), script))
	require.NoError(t, CheckTransactionSanity(btcutil.NewTx(anchor)))
	_, _, err := CheckTransactionInputs(btcutil.NewTx(anchor), false, 1, NewUtxoViewpoint(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	view := NewUtxoViewpoint()
	spend := wire.NewMsgTx(2)
	for _, i := range []uint32{0, 2} {
		point := wire.OutPoint{Hash: anchor.TxHash(), Index: i}
		view.entries[point] = NewUtxoEntry(anchor.TxOut[i], 1, false)
		spend.AddTxIn(wire.NewTxIn(&point, nil, nil))
	}
	spend.AddTxOut(wire.NewTxOut(21000, assets.Clone(), script))
	require.NoError(t, CheckTransactionSanity(btcutil.NewTx(spend)))
	fee, _, err := CheckTransactionInputs(btcutil.NewTx(spend), false, 2, view, &chaincfg.TestNetParams)
	require.NoError(t, err, "legal split must preserve bindings when inputs are merged")
	require.Zero(t, fee)
}
