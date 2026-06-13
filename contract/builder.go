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

type TemplateDeployTxBuildRequest struct {
	ContractPrefix  string
	TemplateName    string
	TemplateVersion uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
	GasLimit        uint64
	Funding         wire.TxOut
	Inputs          []wire.OutPoint
	ChangeOutputs   []*wire.TxOut
}

type TemplateInvokeTxBuildRequest struct {
	Contract      ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Action        string
	Param         []byte
	Funding       wire.TxOut
	Inputs        []wire.OutPoint
	ChangeOutputs []*wire.TxOut
}

type AgentDeployTxBuildRequest struct {
	ContractPrefix  string
	Subtype         string
	AgentVersion    uint32
	Deployer        string
	Random          []byte
	ContractContent []byte
	GasLimit        uint64
	Funding         wire.TxOut
	Inputs          []wire.OutPoint
	ChangeOutputs   []*wire.TxOut
}

type AgentInvokeTxBuildRequest struct {
	Contract      ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Action        string
	Param         []byte
	Funding       wire.TxOut
	Inputs        []wire.OutPoint
	ChangeOutputs []*wire.TxOut
}

type EVMDeployTxBuildRequest struct {
	ContractPrefix string
	Caller         EVMAddress
	GasLimit       uint64
	DeployNonce    uint64
	InitCode       []byte
	Funding        wire.TxOut
	Inputs         []wire.OutPoint
	ChangeOutputs  []*wire.TxOut
}

type EVMInvokeTxBuildRequest struct {
	Contract      ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Calldata      []byte
	Funding       wire.TxOut
	Inputs        []wire.OutPoint
	ChangeOutputs []*wire.TxOut
}

func BuildTemplateDeployTx(req TemplateDeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	templateVersion := req.TemplateVersion
	if templateVersion == 0 {
		templateVersion = CurrentTemplateVersion
	}
	contract, _, err := DeriveTemplateContractAddress(prefix, req.ContractContent, req.Deployer, req.Random)
	if err != nil {
		return nil, ContractAddress{}, err
	}
	if err := validateContractFundingTxOut(req.Funding, true); err != nil {
		return nil, ContractAddress{}, err
	}
	encoded, err := EncodeTemplateDeployPayload(TemplateDeployPayload{
		GasLimit:        req.GasLimit,
		TemplateName:    NormalizeTemplateName(req.TemplateName),
		TemplateVersion: templateVersion,
		Deployer:        req.Deployer,
		Random:          cloneBytes(req.Random),
		ContractContent: cloneBytes(req.ContractContent),
	})
	if err != nil {
		return nil, ContractAddress{}, err
	}
	scripts, err := NullDataScripts(TxTypeDeploy, encoded)
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, contract, nil
}

func BuildTemplateInvokeTx(req TemplateInvokeTxBuildRequest) (*wire.MsgTx, error) {
	if err := validateContractFundingTxOut(req.Funding, true); err != nil {
		return nil, err
	}
	encoded, err := EncodeTemplateInvokePayload(TemplateInvokePayload{
		GasLimit:  req.GasLimit,
		CallNonce: req.CallNonce,
		Action:    req.Action,
		Param:     cloneBytes(req.Param),
	})
	if err != nil {
		return nil, err
	}
	scripts, err := NullDataScripts(TxTypeInvoke, encoded)
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, nil
}

func BuildAgentDeployTx(req AgentDeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	version := req.AgentVersion
	if version == 0 {
		version = CurrentAgentVersion
	}
	contract, _, err := DeriveAgentContractAddress(prefix, req.Subtype, req.ContractContent, req.Deployer, req.Random)
	if err != nil {
		return nil, ContractAddress{}, err
	}
	if err := validateContractFundingTxOut(req.Funding, false); err != nil {
		return nil, ContractAddress{}, err
	}
	encoded, err := EncodeAgentDeployPayload(AgentDeployPayload{
		GasLimit:        req.GasLimit,
		Subtype:         req.Subtype,
		AgentVersion:    version,
		Deployer:        req.Deployer,
		Random:          cloneBytes(req.Random),
		ContractContent: cloneBytes(req.ContractContent),
	})
	if err != nil {
		return nil, ContractAddress{}, err
	}
	scripts, err := NullDataScripts(TxTypeDeploy, encoded)
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, contract, nil
}

func BuildAgentInvokeTx(req AgentInvokeTxBuildRequest) (*wire.MsgTx, error) {
	if err := validateContractFundingTxOut(req.Funding, false); err != nil {
		return nil, err
	}
	encoded, err := EncodeAgentInvokePayload(AgentInvokePayload{
		GasLimit:  req.GasLimit,
		CallNonce: req.CallNonce,
		Action:    req.Action,
		Param:     cloneBytes(req.Param),
	})
	if err != nil {
		return nil, err
	}
	scripts, err := NullDataScripts(TxTypeInvoke, encoded)
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, nil
}

func BuildEVMDeployTx(req EVMDeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	contract, err := DeriveEVMCreateContractAddress(prefix, req.Caller, req.DeployNonce)
	if err != nil {
		return nil, ContractAddress{}, err
	}
	if err := validateEVMFundingTxOut(req.Funding); err != nil {
		return nil, ContractAddress{}, err
	}
	scripts, err := DeployNullDataScripts(DeployPayload{
		GasLimit:    req.GasLimit,
		DeployNonce: req.DeployNonce,
		InitCode:    cloneBytes(req.InitCode),
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, contract, nil
}

func BuildEVMInvokeTx(req EVMInvokeTxBuildRequest) (*wire.MsgTx, error) {
	if err := validateEVMFundingTxOut(req.Funding); err != nil {
		return nil, err
	}
	scripts, err := InvokeNullDataScripts(InvokePayload{
		GasLimit:  req.GasLimit,
		CallNonce: req.CallNonce,
		Calldata:  cloneBytes(req.Calldata),
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
	addTxOutCopies(tx, req.ChangeOutputs)
	return tx, nil
}

func DeriveTemplateContractAddress(prefix string, encodedContract []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := deriveTemplateContractHash(encodedContract, deployer, random)
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	addr, err := NewContractAddressFromHash(prefix, AddressVersionV1, ContractTypeTemplate, hash[:])
	if err != nil {
		return ContractAddress{}, [AddressHashLen]byte{}, err
	}
	return addr, hash, nil
}

func DeriveAgentContractAddress(prefix string, subtype string, content []byte, deployer string, random []byte) (ContractAddress, [AddressHashLen]byte, error) {
	hash, err := deriveAgentContractHash(subtype, content, deployer, random)
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

func deriveTemplateContractHash(encodedContract []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if len(encodedContract) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("template contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("template contract random value is empty")
	}

	var buf bytes.Buffer
	writeHashBytes(&buf, encodedContract)
	writeHashBytes(&buf, []byte(deployer))
	writeHashBytes(&buf, random)
	return sha256.Sum256(buf.Bytes()), nil
}

func deriveAgentContractHash(subtype string, content []byte, deployer string, random []byte) ([AddressHashLen]byte, error) {
	if subtype == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract subtype is empty")
	}
	if len(content) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract content is empty")
	}
	if deployer == "" {
		return [AddressHashLen]byte{}, errors.New("agent contract deployer is empty")
	}
	if len(random) == 0 {
		return [AddressHashLen]byte{}, errors.New("agent contract random value is empty")
	}

	var buf bytes.Buffer
	writeHashBytes(&buf, []byte(subtype))
	writeHashBytes(&buf, content)
	writeHashBytes(&buf, []byte(deployer))
	writeHashBytes(&buf, random)
	return sha256.Sum256(buf.Bytes()), nil
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
