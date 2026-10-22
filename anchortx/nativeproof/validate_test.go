package nativeproof

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type proofFacts struct {
	witnesses     map[[32]byte]consignment.WitnessEvidence
	spent         bool
	unknown       bool
	outpointCalls int
}

func (r *proofFacts) ResolveRGB11Witness(txid [32]byte) (consignment.WitnessEvidence, error) {
	if evidence, ok := r.witnesses[txid]; ok {
		return evidence, nil
	}
	return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
}
func (r *proofFacts) ResolveRGB11Outpoint(consignment.Outpoint) (consignment.OutpointEvidence, error) {
	r.outpointCalls++
	if r.unknown {
		return consignment.OutpointEvidence{}, errors.New("no current-chain view")
	}
	return consignment.OutpointEvidence{Known: true, Exists: true, Spent: r.spent}, nil
}

func minedFact(t *testing.T, tx *btcwire.MsgTx) consignment.WitnessEvidence {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, tx.Serialize(&out))
	return consignment.WitnessEvidence{RawTx: out.Bytes(), State: consignment.WitnessMined, BlockHeight: 10, BlockHash: strings.Repeat("1", 64)}
}

func issuedProof(t *testing.T, kind schemas.Kind) ([]byte, Source, *proofFacts, *consignment.Container) {
	t.Helper()
	funding := btcwire.NewMsgTx(2)
	funding.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: chainhash.Hash{42}, Index: 1}, nil, nil))
	script := append([]byte{0, 32}, bytes.Repeat([]byte{2}, 32)...)
	funding.AddTxOut(btcwire.NewTxOut(600, script))
	hash := funding.TxHash()
	first, err := seals.NewGraphBlindSeal(hash[:], 0, 21)
	require.NoError(t, err)
	second, err := seals.NewGraphBlindSeal(hash[:], 0, 22)
	require.NoError(t, err)
	allocations := []issuance.Allocation{{Seal: first, Amount: 12_000}, {Seal: second, Amount: 1_000}}
	precision := uint8(2)
	if kind == schemas.UDA {
		allocations, precision = []issuance.Allocation{{Seal: first, Amount: 1}}, 0
	}
	spec := issuance.Spec{Kind: kind, Network: issuance.BitcoinRegtest, Ticker: "NAT", Name: "Native proof", Precision: precision,
		Allocations: allocations, Timestamp: 1_700_000_001}
	if kind == schemas.IFA {
		control, err := seals.NewGraphBlindSeal(hash[:], 0, 23)
		require.NoError(t, err)
		spec.InflationRights = []issuance.Allocation{{Seal: control, Amount: 50_000}}
	}
	issued, err := issuance.Issue(spec)
	require.NoError(t, err)
	proof, err := BuildRGB11([]byte(issued.Armor), nil)
	require.NoError(t, err)
	id, err := consensus.ParseContractID(issued.ContractID)
	require.NoError(t, err)
	amount := int64(13_000)
	if kind == schemas.UDA {
		amount = 1
	}
	source := Source{Outpoint: (&btcwire.OutPoint{Hash: hash, Index: 0}).String(), PkScript: script,
		Value: 0, BitcoinNetwork: "regtest", Assets: wire.TxAssets{{Name: wire.AssetName{Protocol: ProtocolRGB11,
			Type: indexer.ASSET_TYPE_FT, Ticker: hex.EncodeToString(id[:])}, Amount: *indexer.NewDecimalWithScale(amount, int(precision))}}}
	facts := &proofFacts{witnesses: map[[32]byte]consignment.WitnessEvidence{[32]byte(hash): minedFact(t, funding)}}
	return proof, source, facts, issued.Container
}

func TestNativeRGB11FullSourceAndHistoricalAuthority(t *testing.T) {
	proof, source, facts, _ := issuedProof(t, schemas.NIA)
	result, err := Validate(proof, source, facts, Current)
	require.NoError(t, err)
	require.Equal(t, Current, result.Mode)
	require.Equal(t, source.Assets[0].Name, result.Asset.Name)
	require.Equal(t, 2, result.Asset.Amount.Precision)
	require.Equal(t, "13000", result.Asset.Amount.Value.String())
	require.Equal(t, uint32(0), result.Asset.BindingSat)
	require.Len(t, result.History.HistoricalStates, 2)
	require.Equal(t, int64(600), result.Value)
	facts.spent = true
	_, err = Validate(proof, source, facts, Current)
	require.ErrorContains(t, err, "currently unspent")
	facts.unknown, facts.outpointCalls = true, 0
	historical, err := Validate(proof, source, facts, Historical)
	require.NoError(t, err)
	require.Equal(t, Historical, historical.Mode)
	require.False(t, historical.History.ConsensusValid)
	require.Empty(t, historical.History.CurrentStates)
	require.Zero(t, facts.outpointCalls, "historical replay must not require today's UTXO view")
}

func TestNativeRGB11RejectsMismatchedClaims(t *testing.T) {
	proof, source, facts, _ := issuedProof(t, schemas.NIA)
	for _, test := range []struct {
		name string
		edit func(*Source)
	}{
		{"partial amount", func(s *Source) { s.Assets[0].Amount = *indexer.NewDecimalWithScale(12_000, 2) }},
		{"excess amount", func(s *Source) { s.Assets[0].Amount = *indexer.NewDecimalWithScale(13_001, 2) }},
		{"precision", func(s *Source) { s.Assets[0].Amount = *indexer.NewDecimalWithScale(1_300, 1) }},
		{"identity", func(s *Source) { s.Assets[0].Name.Ticker = strings.Repeat("a", 64) }},
		{"uppercase identity", func(s *Source) { s.Assets[0].Name.Ticker = strings.ToUpper(s.Assets[0].Name.Ticker) }},
		{"binding", func(s *Source) { s.Assets[0].BindingSat = 1 }},
		{"missing asset", func(s *Source) { s.Assets = nil }},
		{"coasset claim", func(s *Source) { s.Assets = append(s.Assets, s.Assets[0]) }},
		{"wrong output", func(s *Source) { s.Outpoint = strings.TrimSuffix(s.Outpoint, ":0") + ":1" }},
		{"locking script", func(s *Source) { s.PkScript = []byte{0x51} }},
		{"value", func(s *Source) { s.Value = 601 }},
		{"network", func(s *Source) { s.BitcoinNetwork = "mainnet" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			claim := source
			claim.Assets = source.Assets.Clone()
			test.edit(&claim)
			result, err := Validate(proof, claim, facts, Current)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
	for _, kind := range []schemas.Kind{schemas.UDA, schemas.IFA} {
		t.Run(string(kind), func(t *testing.T) {
			proof, source, facts, _ := issuedProof(t, kind)
			result, err := Validate(proof, source, facts, Current)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}

func preparedTransfer(t *testing.T) ([]byte, Source, *proofFacts, *btcwire.MsgTx) {
	t.Helper()
	proof, source, facts, container := issuedProof(t, schemas.NIA)
	validated, err := Validate(proof, source, facts, Current)
	require.NoError(t, err)
	id, err := consensus.ParseContractID(container.ContractID)
	require.NoError(t, err)
	var inputs []operations.TransitionInput
	for _, allocation := range validated.History.HistoricalStates {
		ref := allocation.Reference
		inputs = append(inputs, operations.TransitionInput{OperationID: ref.OperationID, AssignmentType: uint16(ref.AssignmentType), Index: ref.Index})
	}
	seal := seals.NewWitnessBlindSeal(0, 32)
	secret, err := seal.Conceal()
	require.NoError(t, err)
	transition, _, err := operations.BuildTransition(operations.TransitionSpec{ContractID: [32]byte(id), Nonce: 99, TransitionType: 10000,
		Inputs: inputs, Outputs: []operations.TransitionOutput{{AssignmentType: 4000, Class: "fungible", Amount: 13_000, SecretSeal: [32]byte(secret)}}})
	require.NoError(t, err)
	bundle, committed, err := operations.BuildBundle(inputs, transition)
	require.NoError(t, err)
	mpc, err := anchors.NewMPCProofFrom([32]byte(id), 0, bytes.NewReader(nil))
	require.NoError(t, err)
	commitment, err := anchors.ConvolveMPC(mpc, [32]byte(id), committed.BundleID)
	require.NoError(t, err)
	previous, err := btcwire.NewOutPointFromString(source.Outpoint)
	require.NoError(t, err)
	witness := btcwire.NewMsgTx(2)
	witness.AddTxIn(btcwire.NewTxIn(previous, nil, nil))
	witness.AddTxOut(btcwire.NewTxOut(500, source.PkScript))
	witness.AddTxOut(btcwire.NewTxOut(0, anchors.OpretScript(commitment)))
	witnessBundle, err := operations.BuildOpretWitnessBundleWithTx(witness, bundle, mpc)
	require.NoError(t, err)
	transfer, err := consignment.BuildTransfer(container, witnessBundle, [][32]byte{[32]byte(secret)})
	require.NoError(t, err)
	armor, err := consignment.EncodeArmor(transfer.Value)
	require.NoError(t, err)
	reveal, err := seal.StrictBytes()
	require.NoError(t, err)
	proof, err = BuildRGB11([]byte(armor), [][]byte{reveal})
	require.NoError(t, err)
	source.Outpoint = (&btcwire.OutPoint{Hash: witness.TxHash(), Index: 0}).String()
	return proof, source, facts, witness
}

func TestNativeRGB11PreparedIsMaterialNotAdmission(t *testing.T) {
	proof, source, facts, witness := preparedTransfer(t)
	facts.unknown = true
	prepared, err := Validate(proof, source, facts, Prepared)
	require.NoError(t, err)
	require.Equal(t, Prepared, prepared.Mode)
	require.Equal(t, 1, prepared.History.PreparedWitnesses)
	require.False(t, prepared.History.ConsensusValid)
	require.False(t, prepared.History.PreparedValid)
	require.Empty(t, prepared.History.CurrentStates)
	for _, mode := range []Mode{Current, Historical} {
		result, err := Validate(proof, source, facts, mode)
		require.Error(t, err)
		require.Nil(t, result)
	}
	facts.witnesses[[32]byte(witness.TxHash())] = consignment.WitnessEvidence{State: consignment.WitnessArchived}
	_, err = Validate(proof, source, facts, Prepared)
	require.ErrorIs(t, err, consignment.ErrWitnessArchived)
	facts.witnesses[[32]byte(witness.TxHash())] = minedFact(t, witness)
	corrupted := facts.witnesses[[32]byte(witness.TxHash())]
	corrupted.RawTx = []byte{1, 2, 3}
	facts.witnesses[[32]byte(witness.TxHash())] = corrupted
	_, err = Validate(proof, source, facts, Prepared)
	require.ErrorIs(t, err, consignment.ErrWitnessMismatch, "prepared material cannot hide contradictory mined evidence")
	facts.witnesses[[32]byte(witness.TxHash())] = minedFact(t, witness)
	facts.unknown = false
	_, err = Validate(proof, source, facts, Current)
	require.NoError(t, err)
	facts.spent, facts.outpointCalls = true, 0
	_, err = Validate(proof, source, facts, Historical)
	require.NoError(t, err)
	require.Zero(t, facts.outpointCalls)
}

func TestNativeProofCanonicalEnvelope(t *testing.T) {
	proof, _, _, _ := issuedProof(t, schemas.NIA)
	decoded, err := Decode(proof)
	require.NoError(t, err)
	rebuilt, err := BuildRGB11(decoded.Consignment, decoded.Reveals)
	require.NoError(t, err)
	require.Equal(t, proof, rebuilt)
	for _, corrupt := range [][]byte{nil, proof[:11], append(append([]byte(nil), proof...), 0), append([]byte("OTHER"), proof[5:]...)} {
		_, err := Decode(corrupt)
		require.Error(t, err)
	}
	first, err := seals.NewWitnessBlindSeal(0, 5).StrictBytes()
	require.NoError(t, err)
	second, err := seals.NewWitnessBlindSeal(1, 6).StrictBytes()
	require.NoError(t, err)
	ordered, err := BuildRGB11(decoded.Consignment, [][]byte{second, first})
	require.NoError(t, err)
	sorted, err := Decode(ordered)
	require.NoError(t, err)
	require.Equal(t, [][]byte{first, second}, sorted.Reveals)
	_, err = BuildRGB11(decoded.Consignment, [][]byte{first, first})
	require.Error(t, err)
}
