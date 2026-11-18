package anchortx

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/strict_types"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/sat20-labs/satoshinet/httpclient"
)

type indexerBitcoinResolver struct{ client *httpclient.IndexerClient }

func (r *indexerBitcoinResolver) CheckRGB11ChainNet(value strict_types.Value) error {
	if r == nil || r.client == nil {
		return consignment.ErrChainNetUnverified
	}
	header, err := r.client.GetBitcoinBlockHeader(0)
	if err != nil {
		return err
	}
	return nativeproof.CheckBitcoinChainNet(value, header.Hash)
}

func (r *indexerBitcoinResolver) ResolveRGB11Witness(txid [32]byte) (consignment.WitnessEvidence, error) {
	id := chainhash.Hash(txid).String()
	status, err := r.client.GetBitcoinTxStatus(id)
	if err != nil {
		return consignment.WitnessEvidence{}, err
	}
	if !status.Exists {
		return consignment.WitnessEvidence{}, consignment.ErrWitnessUnresolved
	}
	if !status.Confirmed || status.InMempool || status.Confirmations <= 0 || status.BlockHeight <= 0 || status.BlockHeight > math.MaxUint32 {
		return consignment.WitnessEvidence{}, fmt.Errorf("native Bitcoin witness is not mined")
	}
	header, err := r.client.GetBitcoinBlockHeader(uint32(status.BlockHeight))
	if err != nil {
		return consignment.WitnessEvidence{}, err
	}
	if header.Confirmations <= 0 || header.Hash != status.BlockHash {
		return consignment.WitnessEvidence{State: consignment.WitnessArchived}, nil
	}
	result, err := r.client.GetBitcoinRawTx(id)
	if err != nil {
		return consignment.WitnessEvidence{}, err
	}
	raw, err := hex.DecodeString(result.RawTx)
	if err != nil {
		return consignment.WitnessEvidence{}, err
	}
	tx := new(btcwire.MsgTx)
	reader := bytes.NewReader(raw)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || [32]byte(tx.TxHash()) != txid {
		return consignment.WitnessEvidence{}, consignment.ErrWitnessMismatch
	}
	return consignment.WitnessEvidence{RawTx: raw, State: consignment.WitnessMined,
		BlockHeight: uint32(status.BlockHeight), BlockHash: status.BlockHash}, nil
}

func (r *indexerBitcoinResolver) ResolveRGB11Outpoint(point consignment.Outpoint) (consignment.OutpointEvidence, error) {
	outpoint := btcwire.OutPoint{Hash: chainhash.Hash(point.TxID), Index: point.Vout}
	status, err := r.client.GetBitcoinOutspend(outpoint.String())
	if err != nil {
		return consignment.OutpointEvidence{}, err
	}
	result := consignment.OutpointEvidence{Known: true, Exists: status.Exists, Spent: status.Spent}
	if status.SpendingTx != "" {
		hash, err := chainhash.NewHashFromStr(status.SpendingTx)
		if err != nil || hash.String() != status.SpendingTx || !status.Spent {
			return consignment.OutpointEvidence{}, fmt.Errorf("invalid Bitcoin spending transaction")
		}
		id := [32]byte(*hash)
		result.SpendingTxID = &id
	}
	return result, nil
}
