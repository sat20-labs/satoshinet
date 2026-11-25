package nativeproof

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/rgb11/strict_types"
	"github.com/sat20-labs/satoshinet/wire"
)

// Mode identifies the authority granted by validation. Historical and
// Prepared are proof-material checks and MUST NOT authorize a new mapping.
type Mode uint8

const (
	Current    Mode = iota + 1 // mined history and an existing, currently unspent source
	Historical                 // mined history; the source may already have been spent
	Prepared                   // embedded, unbroadcast witnesses may be checked as material only
)

// Source is the exact invoice claim. Assets must contain the complete RGB11
// fungible allocation at Outpoint, with the genesis precision and BindingSat 0.
// BitcoinNetwork, when set, binds the genesis chain to the caller's network;
// node admission always sets it. Recognized names are mainnet, testnet3,
// testnet4 (also testnet), signet and regtest.
type Source struct {
	Outpoint       string
	PkScript       []byte
	Value          int64
	Assets         wire.TxAssets
	BitcoinNetwork string
	VerifyName     func(*wire.DKVSRecord, string) (string, error)
}

// Result contains the canonical contract identity and FULL source amount.
// Asset.Name is the authenticated chain name when a name record is present.
// Otherwise it is the full local Contract ID key. Asset.Amount.Precision
// comes from genesis, and Asset.BindingSat is always zero. Only Mode Current
// establishes current source ownership; no mode authorizes replay by itself.
type Result struct {
	Mode           Mode
	ContractID     string
	SchemaID       string
	Asset          wire.AssetInfo
	Outpoint       string
	Value          int64
	PkScript       []byte
	BitcoinNetwork string
	History        consignment.HistoryReport
}

// Validate reuses RGB11's complete history validator, then binds its exact
// allocations to the claimed Bitcoin output. The resolver never supplies RGB
// balances. A failed call returns no usable partial result.
func Validate(raw []byte, source Source, resolver consignment.BitcoinResolver, mode Mode) (*Result, error) {
	if mode != Current && mode != Historical && mode != Prepared {
		return nil, fmt.Errorf("invalid native proof validation mode")
	}
	if source.Value < 0 || len(source.PkScript) == 0 || len(source.Assets) != 1 {
		return nil, fmt.Errorf("native mapping requires one asset and an exact source output")
	}
	claim := source.Assets[0]
	if claim.Name.Protocol != ProtocolRGB11 || claim.Name.Type != indexer.ASSET_TYPE_FT || claim.BindingSat != 0 ||
		claim.Amount.Validate() != nil || claim.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("native mapping requires a positive unbound RGB11 fungible asset")
	}
	outpoint, err := btcwire.NewOutPointFromString(source.Outpoint)
	if err != nil || outpoint.String() != source.Outpoint {
		return nil, fmt.Errorf("noncanonical native source outpoint")
	}
	proof, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	container, err := consignment.DecodeFile(proof.Consignment)
	if err != nil {
		return nil, fmt.Errorf("RGB11 consignment: %w", err)
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil || !descriptor.Fungible {
		return nil, fmt.Errorf("native mapping supports only RGB11 fungible schemas")
	}
	contractID, err := consensus.ParseContractID(container.ContractID)
	if err != nil {
		return nil, err
	}
	name := wire.AssetName{Protocol: ProtocolRGB11, Type: indexer.ASSET_TYPE_FT, Ticker: hex.EncodeToString(contractID[:])}
	genesis, _ := container.Value.Field("genesis")
	schema, types := container.SchemaValues()
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return nil, err
	}
	if proof.NameRecord != nil {
		if source.VerifyName == nil {
			return nil, fmt.Errorf("no RGB11 name authority")
		}
		registeredID, err := source.VerifyName(proof.NameRecord, claim.Name.Ticker)
		if err != nil || registeredID != name.Ticker || claim.Name.Ticker != RGB11ShortTicker(metadata.Ticker, contractID) || source.Value != 0 {
			return nil, fmt.Errorf("invalid public RGB11 name binding or Anchor value")
		}
		name.Ticker = claim.Name.Ticker
	}
	if name != claim.Name {
		return nil, fmt.Errorf("native asset identity does not match RGB11 genesis")
	}
	if claim.Amount.Precision != int(metadata.Precision) {
		return nil, fmt.Errorf("native amount precision does not match RGB11 genesis")
	}
	network, err := bitcoinNetwork(genesis, source.BitcoinNetwork)
	if err != nil {
		return nil, err
	}
	reveals := make([]seals.GraphBlindSeal, 0, len(proof.Reveals))
	for _, raw := range proof.Reveals {
		reveal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, reveal)
	}
	if _, err := container.RevealGraphSeals(reveals); err != nil {
		return nil, err
	}
	evidence := &minedResolver{inner: resolver, witnesses: make(map[[32]byte]consignment.WitnessEvidence)}
	var history consignment.HistoryReport
	if mode == Prepared {
		history, err = container.ValidatePreparedHistoryReport(evidence)
	} else {
		history, err = container.ValidateHistoryReport(evidence)
	}
	if err != nil {
		return nil, fmt.Errorf("RGB11 history: %w", err)
	}
	if evidence.invalid != nil {
		return nil, evidence.invalid
	}
	sourcePoint := consignment.Outpoint{TxID: [32]byte(outpoint.Hash), Vout: outpoint.Index}
	amount := new(big.Int)
	refs := make(map[schemas.InputRef]bool)
	for _, state := range history.HistoricalStates {
		if state.Outpoint != sourcePoint {
			continue
		}
		// Inflation/replacement rights are never fungible token balances.
		if state.Reference.AssignmentType != 4000 || state.State.Class != "fungible" {
			return nil, fmt.Errorf("native source contains unsupported RGB11 control state")
		}
		if refs[state.Reference] {
			return nil, fmt.Errorf("duplicate native source allocation")
		}
		refs[state.Reference] = true
		amount.Add(amount, new(big.Int).SetUint64(state.State.Amount))
	}
	if len(refs) == 0 || amount.Sign() <= 0 || amount.Cmp(claim.Amount.Value) != 0 {
		return nil, fmt.Errorf("native invoice must claim the full validated source amount")
	}
	if mode == Current {
		for _, consumer := range history.ValidatedConsumers {
			if refs[consumer.Input] {
				return nil, fmt.Errorf("native source has a validated RGB11 consumer")
			}
		}
		status, err := evidence.ResolveRGB11Outpoint(sourcePoint)
		if err != nil || !status.Known || !status.Exists || status.Spent || status.SpendingTxID != nil {
			return nil, fmt.Errorf("native source is not currently unspent")
		}
	}
	value, script, err := sourceOutput(sourcePoint, history, evidence, mode)
	if err != nil {
		return nil, err
	}
	if evidence.invalid != nil {
		return nil, evidence.invalid
	}
	if value < source.Value || !bytes.Equal(script, source.PkScript) {
		return nil, fmt.Errorf("native source value or locking script does not match invoice")
	}
	return &Result{Mode: mode, ContractID: container.ContractID, SchemaID: container.SchemaID,
		Asset:    wire.AssetInfo{Name: name, Amount: indexer.Decimal{Precision: int(metadata.Precision), Value: amount}, BindingSat: 0},
		Outpoint: source.Outpoint, Value: value, PkScript: append([]byte(nil), script...), BitcoinNetwork: network, History: history}, nil
}

const RGB11SuffixLen = 4

// RGB11ShortTicker uses the last four hexadecimal digits of the Contract ID.
func RGB11ShortTicker(ticker string, id [32]byte) string {
	return RGB11BaseTicker(ticker) + "@" + hex.EncodeToString(id[len(id)-RGB11SuffixLen/2:])
}

// RGB11BaseTicker canonicalizes the human-readable part of a chain name.
func RGB11BaseTicker(ticker string) string {
	var base strings.Builder
	lastDash := false
	for i := 0; i < len(ticker); i++ {
		char := ticker[i]
		if char >= 'A' && char <= 'Z' {
			char += 'a' - 'A'
		}
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			base.WriteByte(char)
			lastDash = false
		} else if base.Len() > 0 && !lastDash {
			base.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimSuffix(base.String(), "-")
}

func bitcoinNetwork(genesis strict_types.Value, expected string) (string, error) {
	value, ok := genesis.Field("chainNet")
	value = value.Unwrap()
	names := []string{"mainnet", "testnet3", "testnet4", "signet", "regtest"}
	if !ok || value.Kind != strict_types.ValueEnum || int(value.Tag) >= len(names) {
		return "", fmt.Errorf("native proof requires a supported Bitcoin genesis network")
	}
	actual := names[value.Tag]
	if expected == "testnet" {
		expected = "testnet4"
	}
	if expected != "" && actual != expected {
		return "", fmt.Errorf("RGB11 genesis network %s differs from %s", actual, expected)
	}
	return actual, nil
}

// CheckBitcoinChainNet binds the committed network to a fact from the same
// Bitcoin source used for validation. Signets share a genesis and require
// additional challenge evidence; configuration names alone cannot prove them.
func CheckBitcoinChainNet(value strict_types.Value, genesisHash string) error {
	value = value.Unwrap()
	if value.Kind != strict_types.ValueEnum {
		return consignment.ErrChainNetUnverified
	}
	var expected *chainhash.Hash
	switch value.Tag {
	case 0:
		expected = chaincfg.MainNetParams.GenesisHash
	case 1:
		expected = chaincfg.TestNet3Params.GenesisHash
	case 2:
		expected = chaincfg.TestNet4Params.GenesisHash
	case 4:
		expected = chaincfg.RegressionNetParams.GenesisHash
	default:
		return fmt.Errorf("%w: network %s requires unsupported evidence", consignment.ErrChainNetUnverified, value.Name)
	}
	actual, err := chainhash.NewHashFromStr(genesisHash)
	if len(genesisHash) != 64 || err != nil || *actual != *expected {
		return fmt.Errorf("%w: Bitcoin genesis does not match the contract network", consignment.ErrChainNetUnverified)
	}
	return nil
}

// A validation call has one immutable snapshot per transaction. Historical
// validation uses raw mined witnesses and never asks for today's UTXO state.
type minedResolver struct {
	inner     consignment.BitcoinResolver
	witnesses map[[32]byte]consignment.WitnessEvidence
	invalid   error
}

func (r *minedResolver) CheckRGB11ChainNet(value strict_types.Value) error {
	checker, ok := r.inner.(consignment.ChainNetResolver)
	if !ok {
		return consignment.ErrChainNetUnverified
	}
	return checker.CheckRGB11ChainNet(value)
}

func (r *minedResolver) ResolveRGB11Witness(txid [32]byte) (consignment.WitnessEvidence, error) {
	if cached, ok := r.witnesses[txid]; ok {
		return cached, nil
	}
	if r.inner == nil {
		return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
	}
	evidence, err := r.inner.ResolveRGB11Witness(txid)
	// Preserve an explicit archived verdict: prepared validation must not
	// resurrect a reorged transaction from its embedded copy.
	if evidence.State == consignment.WitnessArchived {
		return evidence, nil
	}
	if err != nil {
		return evidence, err
	}
	if evidence.State != consignment.WitnessMined || evidence.BlockHeight == 0 || len(evidence.BlockHash) != 64 {
		if evidence.State == consignment.WitnessMined {
			r.invalid = consignment.ErrWitnessUnresolved
		}
		return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
	}
	blockHash, err := chainhash.NewHashFromStr(evidence.BlockHash)
	if err != nil || *blockHash == (chainhash.Hash{}) {
		r.invalid = consignment.ErrWitnessUnresolved
		return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
	}
	tx := new(btcwire.MsgTx)
	reader := bytes.NewReader(evidence.RawTx)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || [32]byte(tx.TxHash()) != txid {
		r.invalid = consignment.ErrWitnessMismatch
		return consignment.WitnessEvidence{}, consignment.ErrWitnessMismatch
	}
	r.witnesses[txid] = evidence
	return evidence, nil
}

func (r *minedResolver) ResolveRGB11Outpoint(outpoint consignment.Outpoint) (consignment.OutpointEvidence, error) {
	if r.inner == nil {
		return consignment.OutpointEvidence{}, consignment.ErrOutpointUnknown
	}
	return r.inner.ResolveRGB11Outpoint(outpoint)
}

func sourceOutput(point consignment.Outpoint, history consignment.HistoryReport, resolver *minedResolver, mode Mode) (int64, []byte, error) {
	evidence, err := resolver.ResolveRGB11Witness(point.TxID)
	if err == nil && evidence.State == consignment.WitnessMined {
		tx := new(btcwire.MsgTx)
		if err := tx.Deserialize(bytes.NewReader(evidence.RawTx)); err != nil || uint64(point.Vout) >= uint64(len(tx.TxOut)) {
			return 0, nil, fmt.Errorf("native source output is missing")
		}
		output := tx.TxOut[point.Vout]
		if output.Value < 0 {
			return 0, nil, fmt.Errorf("native source value is negative")
		}
		return output.Value, output.PkScript, nil
	}
	if evidence.State == consignment.WitnessArchived {
		return 0, nil, consignment.ErrWitnessArchived
	}
	if mode == Prepared {
		for _, state := range history.PreparedStates {
			if state.Outpoint == point && state.WitnessPrepared && state.OutputValue >= 0 {
				return state.OutputValue, state.OutputScript, nil
			}
		}
	}
	return 0, nil, fmt.Errorf("native source requires mined Bitcoin evidence")
}
