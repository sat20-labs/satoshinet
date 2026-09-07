package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// CheckDeAnchorFeeExemption checks local de-anchor fee policy, not consensus or
// L1 settlement. Callers must first validate transaction sanity, inputs and
// signatures against the same unspent view. Failure only denies the exemption.
func CheckDeAnchorFeeExemption(tx *wire.MsgTx, view *UtxoViewpoint, params *chaincfg.Params) error {
	if tx == nil || view == nil || params == nil || len(tx.TxIn) == 0 || !IsDeAnchorTx(tx) {
		return fmt.Errorf("not a de-anchor fee exemption candidate")
	}

	var descendOutput *wire.TxOut
	var descendPayload, channelPayload []byte
	for _, output := range tx.TxOut {
		ctype, data, err := common.ReadDataFromNullDataScript(output.PkScript)
		if err != nil || (ctype != common.CONTENT_TYPE_DESCENDING && ctype != common.CONTENT_TYPE_CHANNELID) {
			continue
		}
		canonical, err := common.NullDataScript(ctype, data)
		if err != nil || !bytes.Equal(canonical, output.PkScript) {
			return fmt.Errorf("noncanonical de-anchor marker")
		}
		if ctype == common.CONTENT_TYPE_DESCENDING {
			if descendOutput != nil {
				return fmt.Errorf("duplicate descending marker")
			}
			descendOutput, descendPayload = output, data
		} else {
			if channelPayload != nil {
				return fmt.Errorf("duplicate channel marker")
			}
			channelPayload = data
		}
	}
	if descendOutput == nil || channelPayload == nil {
		return fmt.Errorf("missing descending or channel marker")
	}
	if err := checkDescendFeePayload(descendPayload); err != nil {
		return err
	}

	// Legacy and drain markers contain just the address; normal channel
	// operations may append the commitment height.
	channel, height, hasHeight := strings.Cut(string(channelPayload), "-")
	if hasHeight {
		if _, err := strconv.ParseUint(height, 10, 64); err != nil {
			return fmt.Errorf("invalid channel commitment height")
		}
	}
	addr, err := btcutil.DecodeAddress(channel, params)
	if err != nil || !addr.IsForNet(params) {
		return fmt.Errorf("invalid de-anchor channel address")
	}
	if _, ok := addr.(*btcutil.AddressWitnessScriptHash); !ok {
		return fmt.Errorf("de-anchor channel must be P2WSH")
	}
	channelScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return err
	}
	for _, input := range tx.TxIn {
		entry := view.LookupEntry(input.PreviousOutPoint)
		if entry == nil || entry.IsSpent() || !bytes.Equal(entry.PkScript(), channelScript) {
			return fmt.Errorf("de-anchor input does not belong to channel")
		}
	}
	for _, output := range tx.TxOut {
		if output == descendOutput || bytes.Equal(output.PkScript, channelScript) {
			continue
		}
		// Preserve zero-value metadata such as the legacy unstake memo.
		if len(output.PkScript) == 0 || output.PkScript[0] != txscript.OP_RETURN ||
			output.Value != 0 || len(output.Assets) != 0 {
			return fmt.Errorf("de-anchor output is neither channel change nor metadata")
		}
	}
	return nil
}

func checkDescendFeePayload(data []byte) error {
	if len(data) >= 2 && data[0] == common.DESCEND_PAYLOAD_V2_MAGIC_0 && data[1] == common.DESCEND_PAYLOAD_V2_MAGIC_1 {
		payload, err := common.ParseDescendPayload(data)
		if err != nil || payload.Version != 2 {
			return fmt.Errorf("invalid descending v2 payload")
		}
		switch payload.Operation {
		case common.DESCEND_OP_SPLICING_OUT, common.DESCEND_OP_CLOSE, common.DESCEND_OP_FORCE_CLOSE:
		default:
			return fmt.Errorf("invalid descending operation")
		}
		if len(payload.ReturnedOutputVouts) > 9 ||
			(payload.Operation != common.DESCEND_OP_SPLICING_OUT && len(payload.ReturnedOutputVouts) != 0) {
			return fmt.Errorf("invalid descending returned outputs")
		}
		seen := make(map[uint32]struct{}, len(payload.ReturnedOutputVouts))
		for _, vout := range payload.ReturnedOutputVouts {
			if _, exists := seen[vout]; exists {
				return fmt.Errorf("duplicate descending returned output")
			}
			seen[vout] = struct{}{}
		}
		// A zero L1 txid is intentional for force-close residual drains.
		return nil
	}
	if len(data) != 64 {
		return fmt.Errorf("invalid legacy descending txid length")
	}
	if _, err := hex.DecodeString(string(data)); err != nil {
		return fmt.Errorf("invalid legacy descending txid: %w", err)
	}
	return nil
}
