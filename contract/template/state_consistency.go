package template

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON validates the durable relationship between the generic
// OrderType field and the opaque compact invoke parameter payload.
func (s *TemplateRuntimeState) UnmarshalJSON(data []byte) error {
	type rawTemplateRuntimeState TemplateRuntimeState
	var decoded rawTemplateRuntimeState
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	for index := range decoded.Items {
		if err := validateInvokeItemParamConsistency(&decoded.Items[index]); err != nil {
			return fmt.Errorf("invalid invoke item at index %d: %w", index, err)
		}
	}
	*s = TemplateRuntimeState(decoded)
	return nil
}
