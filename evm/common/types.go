package common

import (
	"encoding/hex"
	"fmt"
)

const (
	MainnetContractPrefix = "ca"
	TestnetContractPrefix = "tc"

	AddressVersionV1 byte = 1

	ContractTypeTemplate byte = 1
	ContractTypeEVM      byte = 2
	ContractTypeAgent    byte = 3

	PayloadVersionV1 byte = 1

	SatoshiAssetName = "::"
)

type TxType byte

const (
	TxTypeDeploy TxType = iota + 1
	TxTypeInvoke
	TxTypeResult
	TxTypeCoinbaseStateRoot
)

type ResultStatus byte

const (
	ResultStatusSuccess ResultStatus = iota
	ResultStatusRevert
	ResultStatusOutOfGas
	ResultStatusInvalid
)

type EVMAddress [20]byte

func ParseEVMAddressHex(s string) (EVMAddress, error) {
	var addr EVMAddress
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}
	if len(s) != 40 {
		return addr, fmt.Errorf("evm address hex length must be 40, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return addr, err
	}
	copy(addr[:], b)
	return addr, nil
}

func (a EVMAddress) String() string {
	return "0x" + hex.EncodeToString(a[:])
}

type DeployPayload struct {
	GasLimit    uint64
	DeployNonce uint64
	InitCode    []byte
}

type InvokePayload struct {
	GasLimit  uint64
	CallNonce uint64
	Calldata  []byte
}

type ResultPayload struct {
	Status       ResultStatus
	ResultCount  uint16
	ErrorDigest  [32]byte
	HasErrorInfo bool
}

type StateRootPayload struct {
	StateRoot [32]byte
}
