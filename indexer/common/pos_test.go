package common

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestVerifyStandardCoinbaseScriptAcceptsEightByteNonce(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	for _, nonce := range []int64{math.MaxInt64, -math.MaxInt64} {
		sig := ecdsa.Sign(key, chainhash.HashB(GetScriptSignData(1, uint64(nonce)))).Serialize()
		script, err := txscript.NewScriptBuilder().AddInt64(1).AddInt64(nonce).AddData(sig).Script()
		require.NoError(t, err)
		require.NoError(t, VerifyStandardCoinbaseScript(script, key.PubKey().SerializeCompressed()))
	}
}

func TestVerifyStandardCoinbaseScriptRejectsOversizedNumbers(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	sig := ecdsa.Sign(key, chainhash.HashB(GetScriptSignData(1, 0))).Serialize()
	for _, field := range []string{"height", "nonce"} {
		for _, negative := range []bool{false, true} {
			name := field + "/positive"
			if negative {
				name = field + "/negative"
			}
			t.Run(name, func(t *testing.T) {
				oversized := make([]byte, 9)
				if field == "height" {
					oversized[0] = 1
				}
				if negative {
					oversized[8] = 0x80
				}
				builder := txscript.NewScriptBuilder()
				if field == "height" {
					builder.AddData(oversized).AddInt64(0)
				} else {
					builder.AddInt64(1).AddData(oversized)
				}
				script, err := builder.AddData(sig).Script()
				require.NoError(t, err)
				require.NotPanics(t, func() {
					require.Error(t, VerifyStandardCoinbaseScript(script, key.PubKey().SerializeCompressed()))
				})
			})
		}
	}
}

func TestPOSProducerAndSubstituteRewards(t *testing.T) {
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	keys := make([]*btcec.PrivateKey, 4)
	pubs := make([]string, 4)
	for i := range keys {
		keys[i], _ = btcec.PrivKeyFromBytes(bytes.Repeat([]byte{byte(i + 1)}, 32))
		pubs[i] = hex.EncodeToString(keys[i].PubKey().SerializeCompressed())
	}
	bootstrap := NewCoreNodeInfo(nil)
	core := NewCoreNodeInfo(nil)
	core.ServerNode = pubs[0]
	core.ChildMiners[pubs[2]] = &MinerAscendInfo{}
	seq := NewMiningSequenceMgr(&params)
	if err := seq.Init(map[string]*CoreNodeInfo{pubs[0]: bootstrap, pubs[1]: core}, 0, ""); err != nil {
		t.Fatal(err)
	}
	for height := 1; height <= 3; height++ {
		slot := seq.GetCurrentMiningInfo()
		for producer := height - 1; producer >= 0; producer-- {
			sig := ecdsa.Sign(keys[producer], chainhash.HashB(GetScriptSignData(height, 0))).Serialize()
			script, err := txscript.NewScriptBuilder().AddInt64(int64(height)).AddInt64(0).AddData(sig).Script()
			if err != nil {
				t.Fatal(err)
			}
			tx := wire.NewMsgTx(1)
			tx.AddTxIn(&wire.TxIn{SignatureScript: script})
			tx.AddTxOut(&wire.TxOut{})
			gotProducer, gotBootstrap, reward, err := seq.POSMiningInfo(tx, height)
			if err != nil {
				t.Fatal(err)
			}
			expected := slot.MiningAddress
			if height == 3 && producer == 0 {
				expected = slot.Father.MiningAddress
			}
			if gotProducer != pubs[producer] || gotBootstrap != pubs[0] || reward != expected {
				t.Fatalf("height=%d producer=%d result=%s/%s/%s expected reward=%s", height, producer, gotProducer, gotBootstrap, reward, expected)
			}
			if err := seq.CheckMiningAddr(tx, height, reward); err != nil {
				t.Fatal(err)
			}
			if err := seq.CheckMiningAddr(tx, height, "wrong address"); err == nil {
				t.Fatal("wrong reward accepted")
			}
			if generated, err := seq.POSReward(height, pubs[producer]); err != nil || generated != reward {
				t.Fatalf("generation and validation disagree: %s %v", generated, err)
			}
		}
		if _, err := seq.POSReward(height, pubs[3]); err == nil {
			t.Fatal("another group may substitute")
		}
		if err := seq.MoveMiningAddr(height, slot.MiningAddress); err != nil {
			t.Fatal(err)
		}
	}
}
