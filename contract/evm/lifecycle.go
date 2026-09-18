package evm

import (
	"fmt"

	gethcommon "github.com/ethereum/go-ethereum/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

type ContractFlags = contractcommon.ContractFlags

// RegisterContractDeployment installs immutable metadata after a successful
// top-level CREATE. It is outside Solidity storage and cannot be replaced by
// another deployment, SSTORE, or an invocation parameter.
func (s *MemoryStateDB) RegisterContractDeployment(addr gethcommon.Address,
	deployer string, flags ContractFlags) error {

	if err := flags.Validate(); err != nil {
		return err
	}
	if deployer == "" {
		return fmt.Errorf("missing EVM deployer")
	}
	acct := s.account(addr)
	if acct == nil {
		return fmt.Errorf("missing deployed EVM account")
	}
	if acct.DeployerAddr != "" || acct.Closed {
		return fmt.Errorf("EVM deployment metadata already registered")
	}
	previousDeployer, previousFlags := acct.DeployerAddr, acct.DeployFlags
	s.appendJournal(func() {
		acct.DeployerAddr = previousDeployer
		acct.DeployFlags = previousFlags
	})
	acct.DeployerAddr = deployer
	acct.DeployFlags = flags
	return nil
}

func (s *MemoryStateDB) ContractDeploymentFlags(addr gethcommon.Address) ContractFlags {
	if acct := s.account(addr); acct != nil {
		return acct.DeployFlags
	}
	return 0
}

func (s *MemoryStateDB) ValidateContractClose(addr gethcommon.Address, actor string) error {
	acct := s.account(addr)
	if acct == nil {
		return fmt.Errorf("EVM contract does not exist")
	}
	return contractcommon.ValidateContractClose(acct.DeployFlags, acct.Closed, acct.DeployerAddr, actor)
}

// Closed contracts remain known so new funding can be refunded rather than
// silently abandoned. A caller's ordinary EVM account is not a contract.
func (s *MemoryStateDB) KnownContract(addr gethcommon.Address) bool {
	acct := s.account(addr)
	return acct != nil && (acct.DeployerAddr != "" || acct.Closed || len(acct.Code) != 0)
}
