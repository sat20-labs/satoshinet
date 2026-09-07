package mempool

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestDeAnchorMempoolFeeExemption(t *testing.T) {
	for _, test := range []struct {
		name                                           string
		badPayload, badChannel, badSignature, ordinary bool
		fee                                            int64
		wantError                                      string
	}{
		{name: "valid zero fee"},
		{name: "invalid payload", badPayload: true, wantError: "rate limiter"},
		{name: "channel mismatch", badChannel: true, wantError: "rate limiter"},
		{name: "invalid signature", badSignature: true, wantError: "signature"},
		{name: "ordinary zero fee", ordinary: true, wantError: "rate limiter"},
		{name: "invalid payload pays ordinary fee", badPayload: true, fee: 5000},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness, _, err := newPoolHarness(&chaincfg.TestNetParams)
			if err != nil {
				t.Fatal(err)
			}
			// Deny ordinary free relay, making exemption observable.
			harness.txPool.cfg.Policy.FreeTxRelayLimit = 0
			harness.txPool.cfg.Policy.MaxTxVersion = 2
			harness.txPool.cfg.HashCache = txscript.NewHashCache(10)
			peer, _ := btcec.PrivKeyFromBytes([]byte{2})
			witnessScript, err := txscript.NewScriptBuilder().AddOp(txscript.OP_2).
				AddData(harness.signKey.PubKey().SerializeCompressed()).
				AddData(peer.PubKey().SerializeCompressed()).
				AddOp(txscript.OP_2).AddOp(txscript.OP_CHECKMULTISIG).Script()
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(witnessScript)
			channel, err := btcutil.NewAddressWitnessScriptHash(hash[:], harness.chainParams)
			if err != nil {
				t.Fatal(err)
			}
			pkScript, err := txscript.PayToAddrScript(channel)
			if err != nil {
				t.Fatal(err)
			}
			const value = int64(20000)
			outpoint := wire.OutPoint{Hash: chainhash.Hash{9}}
			harness.chain.utxos.Entries()[outpoint] = blockchain.NewUtxoEntry(wire.NewTxOut(value, nil, pkScript), 1, false)
			tx := wire.NewMsgTx(2)
			tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
			payload, err := common.EncodeDescendPayloadV2(strings.Repeat("1", 64), common.DESCEND_OP_CLOSE, nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.badPayload {
				payload = append(payload, 0)
			}
			descendScript, err := common.NullDataScript(common.CONTENT_TYPE_DESCENDING, payload)
			if err != nil {
				t.Fatal(err)
			}
			channelAddress := channel.EncodeAddress()
			if test.badChannel {
				channelAddress = harness.payAddr.EncodeAddress()
			}
			channelScript, err := common.NullDataScript(common.CONTENT_TYPE_CHANNELID, []byte(channelAddress))
			if err != nil {
				t.Fatal(err)
			}
			if test.ordinary {
				tx.AddTxOut(wire.NewTxOut(value-test.fee, nil, pkScript))
			} else {
				tx.AddTxOut(wire.NewTxOut(value-test.fee, nil, descendScript))
				tx.AddTxOut(wire.NewTxOut(0, nil, channelScript))
			}
			fetcher := txscript.NewCannedPrevOutputFetcher(pkScript, value, nil)
			sigHashes := txscript.NewTxSigHashes(tx, fetcher)
			witness := wire.TxWitness{nil}
			for _, key := range []*btcec.PrivateKey{harness.signKey, peer} {
				sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, 0, value, nil, witnessScript, txscript.SigHashAll, key)
				if err != nil {
					t.Fatal(err)
				}
				witness = append(witness, sig)
			}
			tx.TxIn[0].Witness = append(witness, witnessScript)
			if test.badSignature {
				tx.TxIn[0].Witness[1] = []byte{1}
			}
			_, err = harness.txPool.CheckMempoolAcceptance(btcutil.NewTx(tx))
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantError) {
				t.Fatalf("error=%v, want containing %q", err, test.wantError)
			}
		})
	}
}
