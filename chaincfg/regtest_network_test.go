package chaincfg_test

import (
	"bytes"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"testing"
	"time"
)

func TestRegressionNetworkIdentityAndGenesis(t *testing.T) {
	p := &chaincfg.RegressionNetParams
	if p.Name != "regtest" || p.Net == chaincfg.TestNetParams.Net || p.Net == chaincfg.MainNetParams.Net {
		t.Fatalf("regression network is not isolated: %s %v", p.Name, p.Net)
	}
	hash := p.GenesisBlock.BlockHash()
	if hash.String() != "6231a0965cd1c33058c92cfd266acb80cdfe9ce03828ef415660c84359e1f2cf" {
		t.Fatalf("regtest genesis changed: %v", hash)
	}
	if hash != *p.GenesisHash || hash == *chaincfg.TestNetParams.GenesisHash || hash == *chaincfg.MainNetParams.GenesisHash {
		t.Fatalf("invalid regression genesis identity: %v, configured %v", hash, p.GenesisHash)
	}
	if len(p.GenesisBlock.Transactions) != 1 || p.GenesisBlock.Transactions[0].TxHash() != p.GenesisBlock.Header.MerkleRoot {
		t.Fatal("genesis transaction does not match merkle root")
	}
	if !bytes.Equal(p.GenesisBlock.Transactions[0].TxOut[0].PkScript, chaincfg.TestNetParams.GenesisBlock.Transactions[0].TxOut[0].PkScript) {
		t.Fatal("regtest lost the test bootstrap identity")
	}
	if p.GenesisBlock.Transactions[0] == chaincfg.TestNetParams.GenesisBlock.Transactions[0] {
		t.Fatal("regtest and testnet share mutable genesis transaction")
	}
	if len(p.Checkpoints) != 1 || p.Checkpoints[0].Height != 0 || *p.Checkpoints[0].Hash != hash {
		t.Fatal("regtest must have only its own genesis checkpoint")
	}
	if p.CoinbaseMaturity != 1 || p.BIP0034Height != 1 || p.BIP0065Height != 1 || p.BIP0066Height != 1 || p.TargetTimePerBlock != 2*time.Second {
		t.Fatal("regression activation/maturity/cadence parameters are incorrect")
	}
	if chaincfg.TestNetParams.CoinbaseMaturity != 1 || chaincfg.TestNetParams.TargetTimePerBlock != 10*time.Minute || chaincfg.TestNetParams.Checkpoints[0].Height != 2759 || chaincfg.MainNetParams.TargetTimePerBlock != 10*time.Minute {
		t.Fatal("public network parameters changed")
	}
	address, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{1}, 32), p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := btcutil.DecodeAddress(address.EncodeAddress(), p)
	if err != nil || !decoded.IsForNet(p) {
		t.Fatalf("default bcrt registration missing: %v", err)
	}
}
