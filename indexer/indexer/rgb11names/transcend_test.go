package rgb11names

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/txscript"
)

func TestParseTranscendRegistration(t *testing.T) {
	contractID := fmt.Sprintf("%064x", 7)
	outpoint := fmt.Sprintf("%064x:0", 8)
	base, err := txscript.NewScriptBuilder().
		AddData([]byte(TranscendTemplateName)).
		AddData([]byte("rgb11:f:" + contractID)).
		AddInt64(0).AddInt64(0).Script()
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := EncodeTranscendRegistrationSuffix("USDT", outpoint, "alice")
	if err != nil {
		t.Fatal(err)
	}
	content := append(base, suffix...)
	reg, err := ParseTranscendRegistration("rgb11:f:"+contractID+"_"+TranscendTemplateName, content)
	if err != nil {
		t.Fatal(err)
	}
	if reg.ContractID != contractID || reg.BaseTicker != "usdt" || reg.GenesisOutpoint != outpoint ||
		reg.ProviderDID != "alice" || reg.AssetType != "f" {
		t.Fatalf("unexpected registration: %+v", reg)
	}
}

func TestParseTranscendRegistrationRequiresDescriptorAndMatchingPath(t *testing.T) {
	contractID := fmt.Sprintf("%064x", 9)
	base, _ := txscript.NewScriptBuilder().
		AddData([]byte(TranscendTemplateName)).
		AddData([]byte("rgb11:f:" + contractID)).
		AddInt64(0).AddInt64(0).Script()
	if _, err := ParseTranscendRegistration("rgb11:f:"+contractID+"_"+TranscendTemplateName, base); err != ErrNotFound {
		t.Fatalf("missing descriptor: %v", err)
	}
	suffix, err := EncodeTranscendRegistrationSuffix("USD", fmt.Sprintf("%064x:0", 10), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTranscendRegistration("rgb11:f:"+contractID+"_other.tc", append(base, suffix...)); err != ErrInvalid {
		t.Fatalf("mismatched path accepted: %v", err)
	}
}
