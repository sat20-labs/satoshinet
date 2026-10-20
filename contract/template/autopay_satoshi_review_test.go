package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestReviewAutopaySatoshiExactTransfer(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int64
		bad  bool
	}{
		{"0.0000000000", 0, false}, {"1.0000000000", 1, false},
		{"1.00000000000", 1, false}, {"9223372036854775807.0", 9223372036854775807, false},
		{"0.5", 0, true}, {"1.5", 0, true}, {"-1", 0, true}, {"9223372036854775808", 0, true},
	} {
		t.Run(tc.text, func(t *testing.T) {
			amount, err := framework.ParseDecimalAmountString(tc.text)
			require.NoError(t, err)
			transfer, err := autopayTransfer("recipient", SatoshiAssetName, amount)
			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, transfer.SatValue)
			}
		})
	}
	amount, err := scommon.NewDecimalFromString("1", 10)
	require.NoError(t, err)
	_, err = framework.DecimalToInt64(*amount)
	require.Error(t, err, "the generic conversion policy remains strict")
}

func TestReviewAutopaySatoshiConfigExactness(t *testing.T) {
	c := NewAutopayContract("satoshi-review", "recipient", SatoshiAssetName, "1")
	for _, text := range []string{"1", "1.0", "1.00000000000", "1.5", "0.5"} {
		param, err := (&AutopayConfigInvokeParam{AmountPerBlock: text}).Encode()
		require.NoError(t, err)
		err = c.CheckInvoke(InvokeAPIConfig, param)
		if text == "1.5" || text == "0.5" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
}
