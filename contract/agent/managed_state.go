package agent

// ManagedContractClosed uses the existing lifecycle state; Result transfers
// credit quantities without invoking the recipient contract.
func (s *RuntimeStore) ManagedContractClosed(addr ContractAddress) (bool, error) {
	return s.ContractClosed(addr)
}
