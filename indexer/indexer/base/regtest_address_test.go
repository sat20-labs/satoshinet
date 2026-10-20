package base

import (
	"bytes"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"testing"
)

func TestRegressionAddressScriptRoundTrip(t *testing.T) {
	for _, params := range []*chaincfg.Params{&chaincfg.RegressionNetParams, &chaincfg.TestNetParams, &chaincfg.MainNetParams} {
		addr, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{7}, 32), params)
		if err != nil {
			t.Fatal(err)
		}
		want, err := txscript.PayToAddrScript(addr)
		if err != nil {
			t.Fatal(err)
		}
		b := &BaseIndexer{chaincfgParam: params, addressValueMap: map[string]*indexer.AddressValueV2{addr.EncodeAddress(): {AddressId: 1}}}
		got, err := b.pkScriptForStoredUtxo(&common.UtxoValueInDB{AddressType: uint16(txscript.WitnessV1TaprootTy), AddressIds: []uint64{1}})
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s script mismatch: %x / %x, %v", params.Name, got, want, err)
		}
	}
}
