package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"golang.org/x/crypto/sha3"
)

const AddressHashLen = 32

type DeployTxBuildRequest struct {
	ContractPrefix  string
	Type            byte
	SubType         string
	Version         uint32
	Deployer        string
	DeployNonce     uint64
	ContractContent []byte
	GasLimit        int64
	Inputs          []wire.OutPoint
	Funding         wire.TxOut
	ExtraOutputs    []*wire.TxOut
}

type InvokeTxBuildRequest struct {
	Contract     ContractAddress
	GasLimit     int64
	CallNonce    uint64
	Action       string
	Param        []byte
	Funding      wire.TxOut
	Inputs       []wire.OutPoint
	ExtraOutputs []*wire.TxOut
}

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	contractType := req.Type
	if contractType == 0 {
		return nil, ContractAddress{}, errors.New("contract type is empty")
	}

	subtype := req.SubType
	version := req.Version
	var contract ContractAddress
	var err error
	switch contractType {
	case ContractTypeTemplate:
		subtype = NormalizeTemplateName(subtype)
		if version == 0 {
			version = CurrentTemplateVersion
		}
		contract, _, err = DeriveTemplateContractAddress(prefix, req.ContractContent, req.Deployer, req.DeployNonce)
		if err != nil {
			return nil, ContractAddress{}, err
		}
		if err := validateContractFundingTxOut(req.Funding, true); err != nil {
			return nil, ContractAddress{}, err
		}
	case ContractTypeAgent:
		if version == 0 {
			version = CurrentAgentVersion
		}
		contract, _, err = DeriveAgentContractAddress(prefix, subtype, req.ContractContent, req.Deployer, req.DeployNonce)
		if err != nil {
			return nil, ContractAddress{}, err
		}
		if err := validateContractFundingTxOut(req.Funding, false); err != nil {
			return nil, ContractAddress{}, err
		}
	case ContractTypeEVM:
		if subtype == "" {
			subtype = "sol"
		}
		caller, err := ParseEVMAddressHex(req.Deployer)
		if err != nil {
			return nil, ContractAddress{}, err
		}
		contract, err = DeriveEVMCreateContractAddress(prefix, caller, req.DeployNonce)
		if err != nil {
			return nil, ContractAddress{}, err
		}
		if err := validateEVMFundingTxOut(req.Funding); err != nil {
			return nil, ContractAddress{}, err
		}
	default:
		return nil, ContractAddress{}, fmt.Errorf("unsupported contract type %d", contractType)
	}
	scripts, err := DeployNullDataScripts(DeployPayload{
		Type:            contractType,
		SubType:         subtype,
		Version:         version,
		GasLimit:        req.GasLimit,
		DeployNonce:     req.DeployNonce,
		ContractContent: cloneBytes(req.ContractContent),
	})
	if err != nil {
		return nil, ContractAddress{}, err
	}

	contractOut, err := NewContractTxOut(req.Funding.Value, req.Funding.Assets, contract)
	if err != nil {
		return nil, ContractAddress{}, err
	}

	tx := newUnsignedContractTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addTxOutCopies(tx, req.ExtraOutputs)
	return tx, contract, nil
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	switch req.Contract.ContractType() {
	case ContractTypeTemplate:
		if err := validateContractFundingTxOut(req.Funding, true); err != nil {
			return nil, err
		}
	case ContractTypeAgent:
		if err := validateContractFundingTxOut(req.Funding, false); err != nil {
			return nil, err
		}
	case ContractTypeEVM:
		if err := validateEVMFundingTxOut(req.Funding); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported contract type %d", req.Contract.ContractType())
	}
	action := req.Action
	if req.Contract.ContractType() == ContractTypeEVM && action == "" {
		action = "call"
	}
	if action == "" {
		return nil, errors.New("invoke action is empty")
	}
	scripts, err := InvokeNullDataScripts(InvokePayload{
		GasLimit:  req.GasLimit,
		CallNonce: req.CallNonce,
		Action:    action,
		Param:     cloneBytes(req.Param),
	})
	if err != nil {
		return nil, err
	}
	contractOut, err := NewContractTxOut(req.Funding.Value, req.Funding.Assets, req.Contract)
	if err != nil {
		return nil, err
	}

	tx := newUnsignedContractTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addTxOutCopies(tx, req.ExtraOutputs)
	return tx, nil
}

func DeriveTemplateContractAddress(prefix string, encodedContract []byte, deployer string, deployNonce uint64) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := deriveTemplateContractHash(encodedContract, deployer, deployNonce)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := NewContractAddressFromHash(prefix, AddressVersionV1, ContractTypeTemplate, hash[:])
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveAgentContractAddress(prefix string, subtype string, content []byte, deployer string, deployNonce uint64) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := deriveAgentContractHash(subtype, content, deployer, deployNonce)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := NewContractAddressFromHash(prefix, AddressVersionV1, ContractTypeAgent, hash[:])
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveEVMCreateContractAddress(prefix string, caller EVMAddress, nonce uint64) (ContractAddress, error) {
	payload := rlpEncodeCreateAddress(caller, nonce)
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(payload)
	sum := hash.Sum(nil)
	var addr EVMAddress
	copy(addr[:], sum[len(sum)-20:])
	return NewContractAddress(prefix, AddressVersionV1, ContractTypeEVM, addr)
}

func NewContractTxOut(value int64, assets wire.TxAssets, contract ContractAddress) (*wire.TxOut, error) {
	script, err := ContractPkScript(contract)
	if err != nil {
		return nil, err
	}
	return wire.NewTxOut(value, assets.Clone(), script), nil
}

func MsgTxHex(tx *wire.MsgTx) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("missing transaction")
	}
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

func ParseOutPoint(s string) (wire.OutPoint, error) {
	outpoint, err := wire.NewOutPointFromString(s)
	if err == nil {
		return *outpoint, nil
	}
	hash, hashErr := chainhash.NewHashFromStr(s)
	if hashErr == nil {
		return wire.OutPoint{Hash: *hash, Index: 0}, nil
	}
	return wire.OutPoint{}, err
}

func validateContractFundingTxOut(funding wire.TxOut, requireFunding bool) error {
	return ValidateFundingTxOut(funding, FundingValidation{
		RequireFunding: requireFunding,
	})
}

func validateEVMFundingTxOut(funding wire.TxOut) error {
	return ValidateFundingTxOut(funding, FundingValidation{
		RequireFunding: true,
		ValidateAmount: validateNonNegativeDecimal,
	})
}

func validateNonNegativeDecimal(amount indexercommon.Decimal) error {
	if amount.Value == nil {
		return fmt.Errorf("nil decimal value")
	}
	if amount.Sign() < 0 {
		return fmt.Errorf("negative amount")
	}
	return nil
}

func deriveTemplateContractHash(encodedContract []byte, deployer string, deployNonce uint64) ([AddressHashLen]byte, error) {
	if len(encodedContract) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("template contract deployer is empty")
	}
	var buf bytes.Buffer
	writeHashBytes(&buf, encodedContract)
	writeHashBytes(&buf, []byte(deployer))
	writeHashUint64(&buf, deployNonce)
	return sha256.Sum256(buf.Bytes()), nil
}

func deriveAgentContractHash(subtype string, content []byte, deployer string, deployNonce uint64) ([AddressHashLen]byte, error) {
	if subtype == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract subtype is empty")
	}
	if len(content) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract deployer is empty")
	}
	var buf bytes.Buffer
	writeHashBytes(&buf, []byte(subtype))
	writeHashBytes(&buf, content)
	writeHashBytes(&buf, []byte(deployer))
	writeHashUint64(&buf, deployNonce)
	return sha256.Sum256(buf.Bytes()), nil
}

func writeHashUint64(buf *bytes.Buffer, v uint64) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	buf.Write(tmp[:])
}

func writeHashBytes(buf *bytes.Buffer, data []byte) {
	var lenBuf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:n])
	buf.Write(data)
}

func rlpEncodeCreateAddress(caller EVMAddress, nonce uint64) []byte {
	addr := rlpEncodeBytes(caller[:])
	n := rlpEncodeUint(nonce)
	payload := append(addr, n...)
	return appendRLPListPrefix(payload)
}

func rlpEncodeUint(v uint64) []byte {
	if v == 0 {
		return []byte{0x80}
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte(v)
		v >>= 8
	}
	return rlpEncodeBytes(buf[i:])
}

func rlpEncodeBytes(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return []byte{b[0]}
	}
	if len(b) <= 55 {
		out := []byte{byte(0x80 + len(b))}
		return append(out, b...)
	}
	lenBytes := encodeLength(len(b))
	out := []byte{byte(0xb7 + len(lenBytes))}
	out = append(out, lenBytes...)
	return append(out, b...)
}

func appendRLPListPrefix(payload []byte) []byte {
	if len(payload) <= 55 {
		out := []byte{byte(0xc0 + len(payload))}
		return append(out, payload...)
	}
	lenBytes := encodeLength(len(payload))
	out := []byte{byte(0xf7 + len(lenBytes))}
	out = append(out, lenBytes...)
	return append(out, payload...)
}

func encodeLength(n int) []byte {
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n)
		n >>= 8
	}
	return buf[i:]
}

func newUnsignedContractTx(inputs []wire.OutPoint) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	return tx
}

func addTxOutCopies(tx *wire.MsgTx, outputs []*wire.TxOut) {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		cp := *output
		cp.PkScript = cloneBytes(output.PkScript)
		cp.Assets = output.Assets.Clone()
		tx.AddTxOut(&cp)
	}
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
