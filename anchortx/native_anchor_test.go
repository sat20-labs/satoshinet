package anchortx

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type nativeAnchorFacts struct {
	txid          [32]byte
	raw           []byte
	spent         bool
	archived      bool
	outpointCalls int
}

func (f *nativeAnchorFacts) ResolveRGB11Witness(txid [32]byte) (consignment.WitnessEvidence, error) {
	if txid != f.txid {
		return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
	}
	if f.archived {
		return consignment.WitnessEvidence{State: consignment.WitnessArchived}, nil
	}
	return consignment.WitnessEvidence{RawTx: f.raw, State: consignment.WitnessMined,
		BlockHeight: 10, BlockHash: strings.Repeat("3", 64)}, nil
}
func (f *nativeAnchorFacts) ResolveRGB11Outpoint(point consignment.Outpoint) (consignment.OutpointEvidence, error) {
	f.outpointCalls++
	return consignment.OutpointEvidence{Known: true, Exists: point.TxID == f.txid && point.Vout == 0, Spent: f.spent}, nil
}

func nativeAnchor(t *testing.T) (*wire.MsgTx, *nativeAnchorFacts) {
	t.Helper()
	core, peer := testAnchorKeys(t)
	witness, channelScript, err := GetP2WSHscript(core.PubKey().SerializeCompressed(), peer.PubKey().SerializeCompressed())
	require.NoError(t, err)
	funding := btcwire.NewMsgTx(2)
	funding.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: btcchainhash.Hash{7}, Index: 1}, nil, nil))
	funding.AddTxOut(btcwire.NewTxOut(600, channelScript))
	hash := funding.TxHash()
	seal, err := seals.NewGraphBlindSeal(hash[:], 0, 21)
	require.NoError(t, err)
	issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA, Network: issuance.BitcoinTestnet4,
		Ticker: "NAT", Name: "STP native anchor", Precision: 2, Timestamp: 1_700_000_001,
		Allocations: []issuance.Allocation{{Seal: seal, Amount: 12_345}}})
	require.NoError(t, err)
	proof, err := nativeproof.BuildRGB11([]byte(issued.Armor), nil)
	require.NoError(t, err)
	id, err := consensus.ParseContractID(issued.ContractID)
	require.NoError(t, err)
	assets := wire.TxAssets{{Name: wire.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT,
		Ticker: hex.EncodeToString(id[:])}, Amount: *indexer.NewDecimalWithScale(12_345, 2)}}
	var serialized bytes.Buffer
	require.NoError(t, funding.Serialize(&serialized))
	facts := &nativeAnchorFacts{txid: [32]byte(hash), raw: serialized.Bytes()}
	require.True(t, StartAnchorManager(&AnchorConfig{ChainParams: &chaincfg.TestNetParams,
		IndexerScheme: "http", IndexerHost: "unused.invalid", IndexerProxy: "testnet", NativeAssetResolver: facts}))
	t.Cleanup(Stop)
	outpoint := (&btcwire.OutPoint{Hash: hash, Index: 0}).String()
	unsigned, err := common.StandardAnchorScriptWithSig(outpoint, witness, 0, assets, nil)
	require.NoError(t, err)
	tx := wire.NewMsgTx(wire.TxVersion)
	input := wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, unsigned, nil)
	input.Sequence = wire.AnchorTxOutIndex
	tx.AddTxIn(input)
	tx.AddTxOut(wire.NewTxOut(0, assets, channelScript))
	addAscendingTicker(t, tx, assets)
	require.NoError(t, common.SetNativeAssetProof(tx, proof))
	signNativeAnchor(t, tx)
	return tx, facts
}

func signNativeAnchor(t *testing.T, tx *wire.MsgTx) {
	t.Helper()
	core, _ := testAnchorKeys(t)
	info, err := ParseAnchorScript(tx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	// Native invoices force output binding even while POS v2 is unscheduled.
	invoice, err := common.AnchorInvoice(tx, false)
	require.NoError(t, err)
	tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(info.Utxo, info.WitnessScript,
		info.Value, info.TxAssets, ecdsa.Sign(core, chainhash.HashB(invoice)).Serialize())
	require.NoError(t, err)
}

func TestNativeAnchorAdmissionAndHistoricalReplay(t *testing.T) {
	tx, facts := nativeAnchor(t)
	_, err := CheckAnchorTxValid(tx, true, false)
	require.ErrorContains(t, err, "not active")
	_, err = CheckAnchorTxValid(tx, true, false, false)
	require.ErrorContains(t, err, "not active")
	info, err := CheckAnchorTxValid(tx, true, false, true)
	require.NoError(t, err)
	require.Equal(t, tx.TxOut[0].Assets, info.TxAssets)
	require.Zero(t, info.Value, "the BTC carrier remains on L1 for a native asset-only mapping")
	_, err = CheckAnchorPkScript(tx.TxIn[0].SignatureScript, false, tx.TxOut, true)
	require.ErrorContains(t, err, "complete native anchor proof")
	facts.spent = true
	_, err = CheckAnchorTxValid(tx, true, false, true)
	require.ErrorContains(t, err, "currently unspent")
	facts.outpointCalls = 0
	_, err = CheckAnchorTxValid(tx, false, false, true)
	require.NoError(t, err)
	locked, err := GetLockedTxInfo(tx, false, false, true)
	require.NoError(t, err)
	require.Equal(t, info.Utxo, locked.Utxo)
	require.Zero(t, facts.outpointCalls)
	facts.archived = true
	_, err = CheckAnchorTxValid(tx, false, false, true)
	require.Error(t, err)
}

func TestNativeAnchorProofAndInvoiceCannotBeSubstituted(t *testing.T) {
	tx, _ := nativeAnchor(t)
	for _, test := range []struct {
		name   string
		mutate func(*wire.MsgTx)
	}{
		{"missing witness", func(tx *wire.MsgTx) { tx.TxIn[0].Witness = nil }},
		{"missing commitment", func(tx *wire.MsgTx) { tx.TxOut = tx.TxOut[:len(tx.TxOut)-1] }},
		{"both missing", func(tx *wire.MsgTx) { tx.TxIn[0].Witness = nil; tx.TxOut = tx.TxOut[:len(tx.TxOut)-1] }},
		{"tampered proof", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[0][12] ^= 1 }},
		{"extra witness", func(tx *wire.MsgTx) { tx.TxIn[0].Witness = append(tx.TxIn[0].Witness, []byte{1}) }},
		{"duplicate commitment", func(tx *wire.MsgTx) { tx.AddTxOut(tx.TxOut[len(tx.TxOut)-1]) }},
		{"funded commitment", func(tx *wire.MsgTx) { tx.TxOut[len(tx.TxOut)-1].Value = 1 }},
		{"trailing commitment", func(tx *wire.MsgTx) {
			tx.TxOut[len(tx.TxOut)-1].PkScript = append(tx.TxOut[len(tx.TxOut)-1].PkScript, 0x51)
		}},
		{"output destination", func(tx *wire.MsgTx) { tx.TxOut[0].PkScript = []byte{0x51} }},
		{"noncanonical envelope", func(tx *wire.MsgTx) { tx.LockTime = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := tx.Copy()
			test.mutate(mutated)
			_, err := CheckAnchorTxValid(mutated, true, false, true)
			require.Error(t, err)
		})
	}
	proof, present, err := common.NativeAssetProof(tx)
	require.NoError(t, err)
	require.True(t, present)
	unsigned := tx.Copy()
	unsigned.TxIn[0].Witness = nil
	unsigned.TxOut = unsigned.TxOut[:len(unsigned.TxOut)-1]
	require.ErrorContains(t, common.SetNativeAssetProof(unsigned, proof), "before signing")
}

func TestNativeAnchorRejectsResignedPartialClaim(t *testing.T) {
	tx, _ := nativeAnchor(t)
	info, err := ParseAnchorScript(tx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	info.TxAssets[0].Amount = *indexer.NewDecimalWithScale(12_000, 2)
	tx.TxOut[0].Assets = info.TxAssets.Clone()
	tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(info.Utxo, info.WitnessScript, info.Value, info.TxAssets, nil)
	require.NoError(t, err)
	signNativeAnchor(t, tx)
	_, err = CheckAnchorTxValid(tx, true, false, true)
	require.ErrorContains(t, err, "full validated source amount")
}
