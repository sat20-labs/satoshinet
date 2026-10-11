package template

import (
	"fmt"
	scommon "github.com/sat20-labs/indexer/common"
)

// Preserve the amount normalization previously performed by InvokeItem's
// JSON reader. Work on a value copy; never mutate caller-owned decimals.
func normalizeCompactInvokeItem(item *InvokeItem) error {
	fields := []struct {
		name  string
		value **scommon.Decimal
		gas   bool
	}{
		{"gasFee", &item.GasFee, true}, {"inAmt", &item.InAmt, false},
		{"retainedAssetA", &item.RetainedAssetA, false}, {"retainedAssetB", &item.RetainedAssetB, false},
		{"remainingAmt", &item.RemainingAmt, false}, {"outAmt", &item.OutAmt, false},
	}
	for _, field := range fields {
		value := *field.value
		if value == nil {
			continue
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("invalid %s: %w", field.name, err)
		}
		text := decimalString(value)
		if text == "" {
			*field.value = nil
			continue
		}
		var err error
		if field.gas {
			*field.value, err = parseGasStateDecimal(field.name, text)
		} else {
			*field.value, err = parseStateDecimal(field.name, text)
		}
		if err != nil {
			return err
		}
	}
	return validateInvokeItemParamConsistency(item)
}
