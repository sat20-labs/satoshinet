package txscript

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func segwitV0Assets(kind string) wire.TxAssets {
	if kind == "empty" {
		return nil
	}
	a := wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "token"}, Amount: *indexer.NewDecimal(11, 0), BindingSat: 2}}
	if kind == "multi" {
		amount, _ := indexer.NewDecimalFromString("1.25", 2)
		a[0].Amount = *amount
		a = append(a, wire.AssetInfo{Name: wire.AssetName{Protocol: "runes", Type: "ft", Ticker: "z"}, Amount: *indexer.NewDecimal(3, 0)})
	}
	return a
}

func segwitV0Tx() *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	for i, b := range []byte{0x11, 0x22} {
		var hash chainhash.Hash
		for j := range hash {
			hash[j] = b
		}
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: hash, Index: uint32(i + 1)}, Sequence: uint32(0xfffffffd + i)})
	}
	tx.AddTxOut(wire.NewTxOut(9000, wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "out"}, Amount: *indexer.NewDecimal(2, 0)}}, []byte{OP_TRUE}))
	tx.AddTxOut(wire.NewTxOut(8000, nil, []byte{OP_TRUE}))
	tx.LockTime = 17
	return tx
}

func TestSatoshiNetSegwitV0FrozenVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/satoshinet-segwit-v0.json")
	require.NoError(t, err)
	var fixture struct {
		Vectors []struct {
			ScriptKind string `json:"script_kind"`
			AssetKind  string `json:"asset_kind"`
			HashType   uint32 `json:"hash_type"`
			Preimage   string
			Hash       string
		}
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Vectors, 36)
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	for _, v := range fixture.Vectors {
		t.Run(fmt.Sprintf("%s/%s/%x", v.ScriptKind, v.AssetKind, v.HashType), func(t *testing.T) {
			preimage, err := hex.DecodeString(v.Preimage)
			require.NoError(t, err)
			first := sha256.Sum256(preimage)
			digest := sha256.Sum256(first[:])
			require.Equal(t, v.Hash, hex.EncodeToString(digest[:]))
			script := append([]byte{OP_0, OP_DATA_20}, make([]byte, 20)...)
			for i := 2; i < len(script); i++ {
				script[i] = 0x22
			}
			if v.ScriptKind == "wsh" {
				script = append(append([]byte{OP_DATA_33}, key.PubKey().SerializeCompressed()...), OP_CHECKSIG)
			}
			tx := segwitV0Tx()
			fetcher := NewCannedPrevOutputFetcher(script, 10000, segwitV0Assets(v.AssetKind))
			got, err := CalcWitnessSigHash(script, NewTxSigHashes(tx, fetcher), SigHashType(v.HashType), tx, 1, 10000, segwitV0Assets(v.AssetKind))
			require.NoError(t, err)
			require.Equal(t, v.Hash, hex.EncodeToString(got))
		})
	}
}

func TestSatoshiNetSegwitV0SignedFields(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	for _, kind := range []string{"wpkh", "wsh"} {
		witnessScript := append(append([]byte{OP_DATA_33}, key.PubKey().SerializeCompressed()...), OP_CHECKSIG)
		pkScript := append([]byte{OP_0, OP_DATA_20}, btcutil.Hash160(key.PubKey().SerializeCompressed())...)
		signScript := pkScript
		if kind == "wsh" {
			hash := sha256.Sum256(witnessScript)
			pkScript = append([]byte{OP_0, OP_DATA_32}, hash[:]...)
			signScript = witnessScript
		}
		for _, assetKind := range []string{"empty", "single", "multi"} {
			for _, hashType := range []SigHashType{SigHashAll, SigHashNone, SigHashSingle, SigHashAll | SigHashAnyOneCanPay, SigHashNone | SigHashAnyOneCanPay, SigHashSingle | SigHashAnyOneCanPay} {
				t.Run(fmt.Sprintf("%s/%s/%x", kind, assetKind, hashType), func(t *testing.T) {
					tx := segwitV0Tx()
					assets := segwitV0Assets(assetKind)
					fetcher := NewCannedPrevOutputFetcher(pkScript, 10000, assets)
					hashes := NewTxSigHashes(tx, fetcher)
					if kind == "wpkh" {
						w, err := WitnessSignature(tx, hashes, 1, 10000, assets, signScript, hashType, key, true)
						require.NoError(t, err)
						tx.TxIn[1].Witness = w
					} else {
						sig, err := RawTxInWitnessSignature(tx, hashes, 1, 10000, assets, signScript, hashType, key)
						require.NoError(t, err)
						tx.TxIn[1].Witness = wire.TxWitness{sig, witnessScript}
					}
					verify := func(tx *wire.MsgTx, amount int64, assets wire.TxAssets) error {
						fetcher := NewCannedPrevOutputFetcher(pkScript, amount, assets)
						vm, err := NewEngine(pkScript, tx, 1, StandardVerifyFlags, nil, NewTxSigHashes(tx, fetcher), amount, assets, fetcher)
						if err != nil {
							return err
						}
						return vm.Execute()
					}
					require.NoError(t, verify(tx, 10000, assets))
					require.Error(t, verify(tx, 10001, assets))
					for _, field := range []string{"count", "protocol", "type", "ticker", "amount", "binding", "order"} {
						if len(assets) == 0 && field != "count" || len(assets) < 2 && field == "order" {
							continue
						}
						changed := segwitV0Assets(assetKind)
						switch field {
						case "count":
							changed = append(changed, wire.AssetInfo{Name: wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "extra"}, Amount: *indexer.NewDecimal(1, 0)})
						case "protocol":
							changed[0].Name.Protocol = "other"
						case "type":
							changed[0].Name.Type = "nft"
						case "ticker":
							changed[0].Name.Ticker = "other"
						case "amount":
							changed[0].Amount = *indexer.NewDecimal(12, 0)
						case "binding":
							changed[0].BindingSat++
						case "order":
							changed[0], changed[1] = changed[1], changed[0]
						}
						require.Error(t, verify(tx, 10000, changed), field)
					}
					for output := range tx.TxOut {
						changed := tx.Copy()
						changed.TxOut[output].Value++
						committed := hashType&sigHashMask == SigHashAll || hashType&sigHashMask == SigHashSingle && output == 1
						if committed {
							require.Error(t, verify(changed, 10000, assets))
						} else {
							require.NoError(t, verify(changed, 10000, assets))
						}
					}
					changed := tx.Copy()
					changed.TxIn[0].PreviousOutPoint.Hash[0] ^= 1
					if hashType&SigHashAnyOneCanPay != 0 {
						require.NoError(t, verify(changed, 10000, assets))
					} else {
						require.Error(t, verify(changed, 10000, assets))
					}
				})
			}
		}
	}
}
