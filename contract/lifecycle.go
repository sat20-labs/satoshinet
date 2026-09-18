package contract

import (
	"errors"
	"fmt"
)

// ContractFlags is deployment metadata, not mutable contract storage. Its zero
// value deliberately means closable. Unknown bits must never be ignored.
type ContractFlags uint32

const (
	ContractFlagNonClosable ContractFlags = 1 << iota
	KnownContractFlags                   = ContractFlagNonClosable
)

// Profit sharing applies to earned contract profit, not user principal, LP
// ownership, prediction payouts or unaccounted assets held at the address.
const (
	DeployerProfitBPS  int64 = 7000
	BootstrapProfitBPS int64 = 3000
	TotalProfitBPS     int64 = 10000
)

var (
	ErrContractClosed      = errors.New("contract is closed")
	ErrContractNonClosable = errors.New("contract is permanently non-closable")
	ErrNotContractDeployer = errors.New("invoker is not deployer")
)

// ContractLifecycle is the common read-only policy view exposed by a runtime.
// Runtime storage remains engine-specific, but the executor owns enforcement.
// A business status such as Prediction.Completed is not itself Closed.
type ContractLifecycle struct {
	Deployer string        `json:"deployer"`
	Flags    ContractFlags `json:"flags"`
	Closed   bool          `json:"closed"`
}

func (f ContractFlags) Validate() error {
	if f&^KnownContractFlags != 0 {
		return fmt.Errorf("unknown contract flags %#x", uint32(f))
	}
	return nil
}

func (f ContractFlags) Closable() bool {
	return f.Validate() == nil && f&ContractFlagNonClosable == 0
}

func (l ContractLifecycle) CheckInvoke(action, actor string) error {
	if err := l.Flags.Validate(); err != nil {
		return err
	}
	if action == ContractInvokeAPIClose {
		return ValidateContractClose(l.Flags, l.Closed, l.Deployer, actor)
	}
	if l.Closed {
		return ErrContractClosed
	}
	return nil
}

// ValidateContractClose is the shared authorization rule for every runtime.
// Successful authorization does not waive the obligation to settle users first.
func ValidateContractClose(flags ContractFlags, closed bool, deployer, actor string) error {
	if err := flags.Validate(); err != nil {
		return err
	}
	if closed {
		return ErrContractClosed
	}
	if !flags.Closable() {
		return ErrContractNonClosable
	}
	if deployer == "" || actor == "" || actor != deployer {
		return ErrNotContractDeployer
	}
	return nil
}
