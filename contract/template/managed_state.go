package template

// ManagedContractClosed adapts the existing lifecycle view to the common
// quantity-settlement interface. It adds no independent state.
func (s *RuntimeStore) ManagedContractClosed(addr ContractAddress) (bool, error) {
	return s.ContractClosed(addr)
}
