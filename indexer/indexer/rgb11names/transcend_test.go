package rgb11names

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
)

func TestParseTranscendRegistration(t *testing.T) {
	params := &chaincfg.TestNetParams
	addr, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{2}, 20), params)
	if err != nil { t.Fatal(err) }
	id := fmt.Sprintf("%064x", 7)
	outpoint := fmt.Sprintf("%064x:0", 8)
	base, err := txscript.NewScriptBuilder().
		AddData([]byte(TranscendTemplateName)).
		AddData([]byte("rgb11:f:"+id)).
		AddInt64(0).AddInt64(0).Script()
	if err != nil { t.Fatal(err) }
	suffix, err := EncodeTranscendRegistrationSuffix(id, "USDT", outpoint, addr.EncodeAddress())
	if err != nil { t.Fatal(err) }
	content := append(base, suffix...)
	reg, err := ParseTranscendRegistration("rgb11:f:"+id+"_"+TranscendTemplateName, content, params)
	if err != nil { t.Fatal(err) }
	if reg.ContractID != id || reg.BaseTicker != "usdt" || reg.GenesisOutpoint != outpoint ||
		reg.GenesisAddress != addr.EncodeAddress() || reg.AssetType != "f" {
		t.Fatalf("unexpected registration: %+v", reg)
	}
}

func TestParseTranscendRegistrationRequiresDescriptorAndMatchingContractID(t *testing.T) {
	params := &chaincfg.TestNetParams
	id := fmt.Sprintf("%064x", 9)
	base, _ := txscript.NewScriptBuilder().
		AddData([]byte(TranscendTemplateName)).
		AddData([]byte("rgb11:f:"+id)).
		AddInt64(0).AddInt64(0).Script()
	if _, err := ParseTranscendRegistration("rgb11:f:"+id+"_"+TranscendTemplateName, base, params); err != ErrNotFound {
		t.Fatalf("missing descriptor: %v", err)
	}
}
