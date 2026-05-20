package common

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

const (
	SAT20_MAGIC_NUMBER = txscript.OP_16
	// 通道
	CONTENT_TYPE_MIN        = txscript.OP_0
	CONTENT_TYPE_CHANNELID  = txscript.OP_0
	CONTENT_TYPE_ASCENDING  = txscript.OP_DATA_1
	CONTENT_TYPE_DESCENDING = txscript.OP_DATA_2
	CONTENT_TYPE_PAYMENT    = txscript.OP_DATA_3
	CONTENT_TYPE_STAKE      = txscript.OP_DATA_4
	CONTENT_TYPE_UNSTAKE    = txscript.OP_DATA_5
	CONTENT_TYPE_DEPOSIT    = txscript.OP_DATA_6
	CONTENT_TYPE_WITHDRAW   = txscript.OP_DATA_7
	CONTENT_TYPE_LIQUIDPOOL = txscript.OP_DATA_8

	// 通道合约
	CONTENT_TYPE_PERFORMACTION  = txscript.OP_DATA_20
	CONTENT_TYPE_DEPLOYCONTRACT = txscript.OP_DATA_21
	CONTENT_TYPE_INVOKECONTRACT = txscript.OP_DATA_22
	CONTENT_TYPE_INVOKERESULT   = txscript.OP_DATA_23

	// 聪网智能合约
	CONTENT_TYPE_CONTRACT_DEPLOY = CONTENT_TYPE_DEPLOYCONTRACT
	CONTENT_TYPE_CONTRACT_INVOKE = CONTENT_TYPE_INVOKECONTRACT
	CONTENT_TYPE_CONTRACT_RESULT = CONTENT_TYPE_INVOKERESULT
	CONTENT_TYPE_CONTRACT_STATE_ROOT = txscript.OP_DATA_24

	CONTENT_TYPE_EVM_DEPLOY = CONTENT_TYPE_CONTRACT_DEPLOY
	CONTENT_TYPE_EVM_INVOKE = CONTENT_TYPE_CONTRACT_INVOKE
	CONTENT_TYPE_EVM_RESULT = CONTENT_TYPE_CONTRACT_RESULT

	// ordx
	CONTENT_TYPE_UNBIND       = txscript.OP_DATA_40
	CONTENT_TYPE_SWAP         = txscript.OP_DATA_41
	CONTENT_TYPE_BINDREFERRER = txscript.OP_DATA_42
	CONTENT_TYPE_FREEZE       = txscript.OP_DATA_43
	CONTENT_TYPE_UNFREEZE     = txscript.OP_DATA_44

	CONTENT_TYPE_MEMO = txscript.OP_DATA_75
	CONTENT_TYPE_MAX  = txscript.OP_DATA_75
	// -> OP_DATA_75

	MAX_PAYLOAD_LEN = txscript.MaxDataCarrierSize - 8
)

type ContractDeployData struct {
	ContractPath    string
	ContractContent []byte
	DeployTime      int64
	LocalSign       []byte
	RemoteSign      []byte
}

type ContractInvokeData struct {
	ContractPath string
	InvokeParam  []byte
	PubKey       []byte
	Sig          []byte
}

func ParseStandardAnchorScript(script []byte) (utxo string, pkScript []byte,
	value int64, assets wire.TxAssets, sig []byte, err error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	// 读取utxo
	if !tokenizer.Next() {
		err = fmt.Errorf("script too short: missing txid")
		return
	}
	utxo = string(tokenizer.Data())

	// 读取pkScript
	if !tokenizer.Next() {
		err = fmt.Errorf("script too short: missing pkScript")
		return
	}
	pkScript = tokenizer.Data()

	// 读取value
	if !tokenizer.Next() {
		err = fmt.Errorf("script too short: missing value")
		return
	}
	value = tokenizer.ExtractInt64()

	// 读取assets
	if !tokenizer.Next() {
		err = fmt.Errorf("script too short: missing assets")
		return
	}
	assetsBuf := tokenizer.Data()
	if assetsBuf != nil {
		err = wire.DeserializeTxAssets(&assets, assetsBuf)
		if err != nil {
			return
		}
	}

	// 读取sig
	if !tokenizer.Next() {
		err = fmt.Errorf("script too short: missing signature")
		return
	}
	sig = tokenizer.Data()
	err = nil

	return
}

func StandardAnchorScript(fundingUtxo string, witnessScript []byte, value int64, assets wire.TxAssets) ([]byte, error) {
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData([]byte(fundingUtxo)).
		AddData(witnessScript).
		AddInt64(int64(value)).
		AddData(assetsBuf).Script()
}

func StandardAnchorScriptWithSig(fundingUtxo string, witnessScript []byte, value int64,
	assets wire.TxAssets, invoiceSig []byte) ([]byte, error) {
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData([]byte(fundingUtxo)).
		AddData(witnessScript).
		AddInt64(int64(value)).
		AddData(assetsBuf).AddData(invoiceSig).Script()
}

// WitnessScriptHash generates a pay-to-witness-script-hash public key script
// paying to a version 0 witness program paying to the passed redeem script.
func WitnessScriptHash(witnessScript []byte) ([]byte, error) {
	P2WSHSize := 1 + 1 + 32
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(P2WSHSize),
	)

	bldr.AddOp(txscript.OP_0)
	scriptHash := sha256.Sum256(witnessScript)
	bldr.AddData(scriptHash[:])
	return bldr.Script()
}

func BytesToPublicKey(pubKeyBytes []byte) (*secp256k1.PublicKey, error) {
	// 检查公钥长度
	if len(pubKeyBytes) != 33 && len(pubKeyBytes) != 65 {
		return nil, fmt.Errorf("invalid public key length: %d", len(pubKeyBytes))
	}

	// 解析公钥
	pubKey, err := secp256k1.ParsePubKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %v", err)
	}

	return pubKey, nil
}

func PublicKeyToTaprootAddress(pubKey *secp256k1.PublicKey, netParams *chaincfg.Params) (*btcutil.AddressTaproot, error) {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	return btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), netParams)
}

func PubKeyBytesToP2TRAddress(pubkey []byte, netParams *chaincfg.Params) (string, error) {
	pbkey, err := BytesToPublicKey(pubkey)
	if err != nil {
		return "", err
	}
	addr, err := PublicKeyToTaprootAddress(pbkey, netParams)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

func VerifyMessage(pubKey *secp256k1.PublicKey, msg []byte, signature *ecdsa.Signature) bool {
	// Compute the hash of the message.
	var msgDigest []byte
	doubleHash := false
	if doubleHash {
		msgDigest = chainhash.DoubleHashB(msg)
	} else {
		msgDigest = chainhash.HashB(msg)
	}

	// Verify the signature using the public key.
	return signature.Verify(msgDigest, pubKey)
}

func NullDataScript(ctype uint8, data []byte) ([]byte, error) {
	if len(data) > MAX_PAYLOAD_LEN {
		return nil, fmt.Errorf("data size %d is larger than max "+
			"allowed size %d", len(data), MAX_PAYLOAD_LEN)
	}

	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_RETURN).
		AddOp(SAT20_MAGIC_NUMBER).
		AddInt64(int64(ctype)).
		AddData(data).Script()
}

func IsSTPNullDataScript(script []byte) bool {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Opcode() != txscript.OP_RETURN {
		// Check for OP_RETURN
		return false
	}

	if !tokenizer.Next() || tokenizer.Err() != nil || tokenizer.Opcode() != SAT20_MAGIC_NUMBER {
		return false
	}

	// content type
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return false
	}
	ctype := tokenizer.ExtractInt64()
	if ctype > CONTENT_TYPE_MAX || ctype < CONTENT_TYPE_MIN {
		return false
	}

	return tokenizer.Next() && tokenizer.Data() != nil &&
		len(tokenizer.Data()) <= MAX_PAYLOAD_LEN
}

func ReadDataFromNullDataScript(script []byte) (uint8, []byte, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	// 检查第一个操作码是否为 OP_RETURN
	if !tokenizer.Next() || tokenizer.Opcode() != txscript.OP_RETURN {
		return 0, nil, fmt.Errorf("script is not OP_RETURN")
	}

	if !tokenizer.Next() || tokenizer.Opcode() != SAT20_MAGIC_NUMBER {
		return 0, nil, fmt.Errorf("script is not STP script")
	}

	// content type
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return 0, nil, fmt.Errorf("script is not STP script")
	}
	ctype := uint8(tokenizer.ExtractInt64())
	if ctype > CONTENT_TYPE_MAX || ctype < CONTENT_TYPE_MIN {
		return 0, nil, fmt.Errorf("invalid type code %d", ctype)
	}

	// 检查是否有数据部分
	if !tokenizer.Next() || tokenizer.Data() == nil {
		return 0, nil, fmt.Errorf("no stp data found in OP_RETURN")
	}

	// 返回数据部分
	return ctype, tokenizer.Data(), nil
}

func GenTickerInfo(data []byte) (*TickerInfo, error) {
	var result TickerInfo
	parts := strings.Split(string(data), "-")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid ascending payload %s", string(data))
	}
	divisibility, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return nil, err
	}
	result.AssetName = *wire.NewAssetNameFromString(parts[0])
	result.MaxSupply, err = indexer.NewDecimalFromString(parts[1], divisibility)
	if err != nil {
		return nil, err
	}
	result.Divisibility = divisibility
	result.N = n

	return &result, nil
}

func ParseSignedDeployContractInvoice(script []byte) (*ContractDeployData, error) {

	tokenizer := txscript.MakeScriptTokenizer(0, script)
	result := ContractDeployData{}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script is missing contract path")
	}
	result.ContractPath = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script is missing contract content")
	}
	result.ContractContent = (tokenizer.Data())

	// deployTime
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script is missing deploy time")
	}
	result.DeployTime = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script too short: missing local sig")
	}
	result.LocalSign = tokenizer.Data()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script too short: missing remote sig")
	}
	result.RemoteSign = tokenizer.Data()

	return &result, nil
}

func ParseSignedInvokeContractInvoice(data []byte) (*ContractInvokeData, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	result := &ContractInvokeData{}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script is missing contract path")
	}
	result.ContractPath = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("script is missing invoke parameter")
	}
	result.InvokeParam = (tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		// 简化的调用方案
		return result, nil
	}
	result.PubKey = tokenizer.Data()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		// 简化的调用方案
		return result, nil
	}
	result.Sig = tokenizer.Data()

	return result, nil
}

// 调用 sindexer.NullDataScript 组装成最终的 op_return 数据
func CreateStakeInvoice(assetName *indexer.AssetName, amt *indexer.Decimal) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(assetName.String())).
		AddData([]byte(amt.String())).
		Script()
}

func ParseStakeInvoice(script []byte) (assetName string,
	amt *indexer.Decimal, err error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	// assetName
	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("script is missing asset name")
		return
	}
	assetName = string(tokenizer.Data())

	// amt
	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("script is missing asset amt")
		return
	}
	amt, err = indexer.NewDecimalFromFormatString(string(tokenizer.Data()))
	if err != nil {
		return
	}

	return
}
