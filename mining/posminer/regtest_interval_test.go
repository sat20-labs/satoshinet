package posminer

import (
	"github.com/sat20-labs/satoshinet/chaincfg"
	"testing"
)

func TestRegressionSchedulingDoesNotMutatePublicIntervals(t *testing.T) {
	oldMiner, oldCheck := MinerInterval, CheckingInterval
	MinerInterval, CheckingInterval = 13, 7
	t.Cleanup(func() { MinerInterval, CheckingInterval = oldMiner, oldCheck })
	reg := &ValidatorManager{cfg: &ValidatorManagerConfig{Config: &Config{ChainParams: &chaincfg.RegressionNetParams}}}
	for _, p := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams} {
		public := &ValidatorManager{cfg: &ValidatorManagerConfig{Config: &Config{ChainParams: p}}}
		if reg.minerInterval() != 2 || reg.checkingInterval() != 1 {
			t.Fatal("regtest schedule is not 2s with 1s checking")
		}
		if public.minerInterval() != 13 || public.checkingInterval() != 7 {
			t.Fatalf("%s lost its existing configured intervals", p.Name)
		}
	}
	if MinerInterval != 13 || CheckingInterval != 7 {
		t.Fatal("regtest changed shared public interval state")
	}
}
