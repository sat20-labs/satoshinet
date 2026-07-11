package template

import "testing"

func TestLimitOrderTradingValueOverflowRejected(t *testing.T) {
	if _, err := calcLimitOrderTradingValue("100000000000000000000", "1"); err == nil {
		t.Fatal("expected overflowing limit order value to be rejected")
	}
}
