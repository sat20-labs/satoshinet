package blockchain

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestCheckDeAnchorFeeExemption(t *testing.T) {
	params := &chaincfg.TestNetParams
	channel, err := btcutil.NewAddressWitnessScriptHash(bytes.Repeat([]byte{1}, 32), params)
	if err != nil {
		t.Fatal(err)
	}
	channelScript, err := txscript.PayToAddrScript(channel)
	if err != nil {
		t.Fatal(err)
	}
	l1TxID := strings.Repeat("1", 64)
	marker := func(ctype uint8, data []byte) []byte {
		t.Helper()
		script, err := common.NullDataScript(ctype, data)
		if err != nil {
			t.Fatal(err)
		}
		return script
	}
	v2 := func(op uint8, vouts ...uint32) []byte {
		t.Helper()
		payload, err := common.EncodeDescendPayloadV2(l1TxID, op, vouts)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	tests := []struct {
		name    string
		payload []byte
		mutate  func(*wire.MsgTx, *UtxoViewpoint)
		wantErr bool
	}{
		{name: "legacy", payload: []byte(l1TxID)},
		{name: "close", payload: v2(common.DESCEND_OP_CLOSE)},
		{name: "splicing out", payload: v2(common.DESCEND_OP_SPLICING_OUT, 0, 2)},
		{name: "force close zero txid", payload: append([]byte{'D', '2', common.DESCEND_OP_FORCE_CLOSE}, make([]byte, 33)...)},
		{name: "commitment height", payload: v2(common.DESCEND_OP_CLOSE), mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut[1].PkScript = marker(common.CONTENT_TYPE_CHANNELID, []byte(channel.EncodeAddress()+"-42"))
		}},
		{name: "channel change", payload: v2(common.DESCEND_OP_SPLICING_OUT), mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut[0].Value -= 330
			tx.AddTxOut(wire.NewTxOut(330, nil, channelScript))
		}},
		{name: "legacy metadata", payload: []byte(l1TxID), mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.AddTxOut(wire.NewTxOut(0, nil, marker(common.CONTENT_TYPE_UNSTAKE, []byte("memo"))))
		}},
		{name: "invalid legacy", payload: []byte("not-a-txid"), wantErr: true},
		{name: "nonhex legacy", payload: []byte(strings.Repeat("z", 64)), wantErr: true},
		{name: "truncated v2", payload: []byte("D2"), wantErr: true},
		{name: "v2 length mismatch", payload: append(v2(common.DESCEND_OP_CLOSE), 0), wantErr: true},
		{name: "unknown operation", payload: v2(255), wantErr: true},
		{name: "duplicate returned output", payload: v2(common.DESCEND_OP_SPLICING_OUT, 1, 1), wantErr: true},
		{name: "close with returned output", payload: v2(common.DESCEND_OP_CLOSE, 1), wantErr: true},
		{name: "missing channel", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut = tx.TxOut[:1]
		}},
		{name: "duplicate descending", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.AddTxOut(wire.NewTxOut(330, nil, tx.TxOut[0].PkScript))
		}},
		{name: "duplicate channel", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.AddTxOut(wire.NewTxOut(0, nil, tx.TxOut[1].PkScript))
		}},
		{name: "trailing marker opcode", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut[0].PkScript = append(tx.TxOut[0].PkScript, txscript.OP_TRUE)
		}},
		{name: "invalid height", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut[1].PkScript = marker(common.CONTENT_TYPE_CHANNELID, []byte(channel.EncodeAddress()+"-bad"))
		}},
		{name: "channel mismatch", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, view *UtxoViewpoint) {
			view.LookupEntry(tx.TxIn[0].PreviousOutPoint).pkScript = []byte{txscript.OP_TRUE}
		}},
		{name: "missing input", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, view *UtxoViewpoint) {
			view.RemoveEntry(tx.TxIn[0].PreviousOutPoint)
		}},
		{name: "spent input", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, view *UtxoViewpoint) {
			view.LookupEntry(tx.TxIn[0].PreviousOutPoint).Spend()
		}},
		{name: "external change", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.AddTxOut(wire.NewTxOut(330, nil, []byte{txscript.OP_TRUE}))
		}},
		{name: "funded channel marker", payload: []byte(l1TxID), wantErr: true, mutate: func(tx *wire.MsgTx, _ *UtxoViewpoint) {
			tx.TxOut[1].Value = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := wire.NewMsgTx(2)
			outpoint := wire.OutPoint{Hash: chainhash.Hash{1}}
			tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
			tx.AddTxOut(wire.NewTxOut(1000, nil, marker(common.CONTENT_TYPE_DESCENDING, test.payload)))
			tx.AddTxOut(wire.NewTxOut(0, nil, marker(common.CONTENT_TYPE_CHANNELID, []byte(channel.EncodeAddress()))))
			view := NewUtxoViewpoint()
			view.Entries()[outpoint] = NewUtxoEntry(wire.NewTxOut(1000, nil, channelScript), 1, false)
			if test.mutate != nil {
				test.mutate(tx, view)
			}
			err := CheckDeAnchorFeeExemption(tx, view, params)
			if (err != nil) != test.wantErr {
				t.Fatalf("CheckDeAnchorFeeExemption error=%v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}
