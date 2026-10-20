package dkvs

import (
	"bytes"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"testing"
)

func TestRegressionAutopayUsesSameKeyWithLocalAddress(t *testing.T) {
	reg := NetworkDefaultsForParams(&chaincfg.RegressionNetParams)
	test := NetworkDefaultsForParams(&chaincfg.TestNetParams)
	regAddr, err := btcutil.DecodeAddress(reg.AutopayDeployer, &chaincfg.RegressionNetParams)
	if err != nil || !regAddr.IsForNet(&chaincfg.RegressionNetParams) {
		t.Fatalf("invalid reg deployer: %v", err)
	}
	testAddr, err := btcutil.DecodeAddress(test.AutopayDeployer, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(regAddr.ScriptAddress(), testAddr.ScriptAddress()) {
		t.Fatal("reg changed autopay key")
	}
	if reg.AutopayContract == "" || reg.AutopayContract == test.AutopayContract || reg.AutopayContract != reg.deriveAutopayContract(&chaincfg.RegressionNetParams) {
		t.Fatal("reg autopay contract not derived from its deployer")
	}
	if test.AutopayDeployer != DefaultTestNetAutopayDeployer || NetworkDefaultsForParams(&chaincfg.MainNetParams).Enabled {
		t.Fatal("public defaults changed")
	}
}
