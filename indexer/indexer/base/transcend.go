package base

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// 从比特币脚本中提取int64值
func extractScriptInt64(data []byte) int64 {
	if len(data) == 0 {
		return 0
	}

	// 比特币脚本中的整数是最小化编码的
	isNegative := (data[len(data)-1] & 0x80) != 0

	buf := make([]byte, 8)
	copy(buf, data)

	if isNegative {
		buf[len(data)-1] &= 0x7f
	}

	val := int64(binary.LittleEndian.Uint64(buf))
	if isNegative {
		val = -val
	}

	return val
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
	value = extractScriptInt64(tokenizer.Data())

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

// anchorPkScript, err := StandardAnchorScript(txid, witnessScript, amount)
func GenAscendFromAnchorPkScript(anchorPkScript []byte, netParams *chaincfg.Params) (*common.AscendData, error) {

	ascend, err := anchortx.CheckAnchorPkScript(anchorPkScript, false)
	if err != nil {
		return nil, err
	}

	return &common.AscendData{
		FundingUtxo: ascend.Utxo,
		Value:       ascend.Value,
		Assets:      ascend.TxAssets,
		Sig:         ascend.Sig,
		Address:     ascend.Address,
		PubA:        ascend.PubKeyA,
		PubB:        ascend.PubKeyB,
	}, nil
}

func ReadDataFromNullDataScript(script []byte) (uint8, []byte, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	// 检查第一个操作码是否为 OP_RETURN
	if !tokenizer.Next() || tokenizer.Opcode() != txscript.OP_RETURN {
		return 0, nil, fmt.Errorf("script is not OP_RETURN")
	}

	if !tokenizer.Next() || tokenizer.Opcode() != common.SAT20_MAGIC_NUMBER {
		return 0, nil, fmt.Errorf("script is not STP script")
	}

	// content type
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return 0, nil, fmt.Errorf("script is not STP")
	}
	ctype := tokenizer.Opcode()

	// 检查是否有数据部分
	if !tokenizer.Next() || tokenizer.Data() == nil {
		return 0, nil, fmt.Errorf("no data found after OP_RETURN")
	}

	// 返回数据部分
	return ctype, tokenizer.Data(), nil
}

func GenDescend(tx *common.Transaction, index int, descendTxId string) (*common.DescendData, error) {

	var result common.DescendData
	output := tx.Outputs[index]
	result.NullDataUtxo = tx.Txid + ":" + strconv.Itoa(index)
	result.DescendTxId = descendTxId
	result.Value = output.Value
	result.Assets = output.Assets
	result.Address = tx.Inputs[0].Address.Addresses[0]

	return &result, nil
}

func GenTickerInfo(data []byte) (*common.TickerInfo, error) {
	var result common.TickerInfo
	parts := strings.Split(string(data), "-")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid ascending payload %s", string(data))
	}
	precition, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return nil, err
	}
	result.AssetName = *wire.NewAssetNameFromString(parts[0])
	result.MaxSupply = parts[1]
	result.Precition = precition
	result.N = n

	return &result, nil
}
