package chaincfg

import "testing"

func TestNativeAssetActivationIsExplicit(t *testing.T) {
	for _, params := range []*Params{&MainNetParams, &TestNetParams, &RegressionNetParams, &SimNetParams, nil} {
		if params.NativeAssetsActive(1) || params.NativeAssetsActive(2_000_000_000) {
			t.Fatal("default networks must not activate native mappings")
		}
	}
	params := TestNetParams
	params.NativeAssetHeight = 100
	if params.NativeAssetsActive(99) || !params.NativeAssetsActive(100) || !params.NativeAssetsActive(101) {
		t.Fatal("native mapping activation boundary differs")
	}
}
