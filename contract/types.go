package contract

import (
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
)

const (
	MainnetContractPrefix = btcutil.ContractMainnetPrefix
	TestnetContractPrefix = btcutil.ContractTestnetPrefix

	AddressVersionV1 = btcutil.ContractAddressVersionV1

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
	Type            byte
	SubType         string
	Version         uint32
	GasLimit        int64
	DeployNonce     uint64
	ContractContent []byte
}

type InvokePayload struct {
	GasLimit  int64
	CallNonce uint64
	Action    string
	Param     []byte
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

type ContractSummary struct {
	Address        string                 `json:"address"`
	ContractType   string                 `json:"contractType"`
	ContractTypeID byte                   `json:"contractTypeId"`
	Subtype        string                 `json:"subtype,omitempty"`
	Name           string                 `json:"name,omitempty"`
	Version        uint32                 `json:"version,omitempty"`
	Status         string                 `json:"status,omitempty"`
	CreatedHeight  int64                  `json:"createdHeight,omitempty"`
	UpdatedHeight  int64                  `json:"updatedHeight,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

type ContractHistoryRecord struct {
	Kind           string                 `json:"kind"`
	Height         int64                  `json:"height"`
	TxID           string                 `json:"txid,omitempty"`
	Contract       string                 `json:"contract"`
	ContractType   string                 `json:"contractType"`
	ContractTypeID byte                   `json:"contractTypeId"`
	Subtype        string                 `json:"subtype,omitempty"`
	Action         string                 `json:"action,omitempty"`
	Status         string                 `json:"status,omitempty"`
	Actor          string                 `json:"actor,omitempty"`
	GasLimit       int64                  `json:"gasLimit,omitempty"`
	Nonce          uint64                 `json:"nonce,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}
