// Copyright (c) 2024 The sats20 developers

package anchortx

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/httpclient"
	"github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	"github.com/sat20-labs/indexer/common"
)

const (
	// 65 txid,
	// 35 output script, it should be p2tr
	// 8 amount for Anchor
	// 8 extra nonce
	MIN_LEN_ANCHORTX_SCRIPT = 102
	MAX_LEN_ANCHORTX_SCRIPT = 116

	// P2WSHSize 34 bytes
	//	- OP_0: 1 byte
	//	- OP_DATA: 1 byte (WitnessScriptSHA256 length)
	//	- WitnessScriptSHA256: 32 bytes
	P2WSHSize = 1 + 1 + 32
)

type AnchorConfig struct {
	IndexerScheme string
	IndexerHost   string
	IndexerNet    string
	ChainParams   *chaincfg.Params
}
type AnchorManager struct {
	anchorConfig   *AnchorConfig
	superNodeList  map[string][]byte // address > public key
	verifyAnchorTx bool
	quit           chan struct{}
}

// var anchorConfig *AnchorConfig
var anchorManager AnchorManager

// txscript.NewScriptBuilder().AddData(txid).AddData(WitnessScript).
// AddInt64(int64(amount)).AddInt64(int64(extraNonce)).Script()
type AnchorInfo struct {
	Utxo          string         `json:"utxo"`          // the utxo with locked in lnd
	WitnessScript []byte         `json:"witnessScript"` // WitnessScript for locked in lnd
	Value         int64          `json:"value"`         // the amount with locked in lnd
	TxAssets      *wire.TxAssets `json:"txAssets"`      // The assets locked
	Sig           []byte         `json:"sig"`
}

type AscendInfo struct {
	AnchorInfo
	Address string `json:"address"`
	PubKeyA []byte `json:"pubKeyA"` // server node
	PubKeyB []byte `json:"pubKeyB"` // client node
}

func StartAnchorManager(config *AnchorConfig) bool {
	if config == nil {
		return false
	}

	anchorManager.superNodeList = make(map[string][]byte)
	anchorManager.anchorConfig = config
	anchorManager.quit = make(chan struct{})
	log.Debugf("AnchorConfig: IndexerScheme: %s", anchorManager.anchorConfig.IndexerScheme)
	log.Debugf("AnchorConfig: IndexerHost: %s", anchorManager.anchorConfig.IndexerHost)
	log.Debugf("AnchorConfig: IndexerNet: %s", anchorManager.anchorConfig.IndexerNet)
	log.Debugf("AnchorConfig: ChainParams: %s", anchorManager.anchorConfig.ChainParams.Name)

	// Default， the node will check anchor tx
	anchorManager.verifyAnchorTx = true
	if anchorManager.anchorConfig.IndexerHost == "" || anchorManager.anchorConfig.IndexerNet == "" {
		//anchorManager.verifyAnchorTx = false
		//log.Debugf("The node not config indexer infomation, will not check anchor tx")
		return false
	}

	return true
}

func Stop() {
	if anchorManager.quit != nil {
		close(anchorManager.quit)
	}
}

func CheckAnchorTxValid(tx *wire.MsgTx) error {
	if !anchorManager.verifyAnchorTx {
		log.Debugf("Not check anchor tx.")
		return nil
	}

	if len(tx.TxIn) != 1 {
		err := fmt.Errorf("invalid Anchor tx, No Anchor info: %s", tx.TxHash().String())
		return err
	}
	AnchorScript := tx.TxIn[0].SignatureScript

	// Check the Anchor tx has completed, all the assets is locked in lnd will be mapped to sats net only one times
	lockedInfo, err := CheckAnchorPkScript(AnchorScript)
	//	err = fmt.Errorf("invalid Anchor tx <%s>, just for test", tx.TxHash().String()) // Just for test
	if err != nil {
		log.Debugf("invalid Anchor tx, invalid Anchor script: %s, err: %s", tx.TxHash().String(), err.Error())
		return err
	}

	// Check anchor amount is same with locked amount

	anchorAmount := int64(0)
	for _, out := range tx.TxOut {
		anchorAmount += out.Value
	}

	if anchorAmount > lockedInfo.Value {
		err := fmt.Errorf("invalid Anchor tx, Anchor amount(%d) is exceed the locked amount (%d): %s", anchorAmount, lockedInfo.Value, tx.TxHash().String())
		return err
	}

	// Check the Anchor tx is valid
	return nil
}

// The Anchor tx info is record in Anchor tx input script
// return txscript.NewScriptBuilder().AddData(data).AddData(outputScript).
// AddInt64(int64(amount)).AddInt64(int64(extraNonce)).Script()
func GetLockedTxInfo(tx *wire.MsgTx) (*AnchorInfo, error) {
	if len(tx.TxIn) != 1 {
		err := fmt.Errorf("invalid Anchor tx: %s", tx.TxHash().String())
		return nil, err
	}
	AnchorScript := tx.TxIn[0].SignatureScript
	// lenAnchorScript := len(AnchorScript)
	// if lenAnchorScript < MIN_LEN_ANCHORTX_SCRIPT || lenAnchorScript > MAX_LEN_ANCHORTX_SCRIPT {
	// 	err := fmt.Errorf("invalid Anchor tx script: %s", tx.TxHash().String())
	// 	return nil, err
	// }

	fmt.Printf("AnchorScript: %x\n", AnchorScript)

	lockedTxInfo, err := CheckAnchorPkScript(AnchorScript)
	if err != nil {
		err := fmt.Errorf("%s : anchortx[%s]", err.Error(), tx.TxHash().String())
		return nil, err
	}
	return &lockedTxInfo.AnchorInfo, nil
}

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

func ParseAnchorScript(AnchorScript []byte) (*AnchorInfo, error) {

	const scriptVersion = 0
	tokenizer := txscript.MakeScriptTokenizer(scriptVersion, AnchorScript)

	// The First opcode must be a canonical data push, the length of the
	// data push is bounded to 40 by the initial check on overall script
	// length.
	if !tokenizer.Next() ||
		!isCanonicalPush(tokenizer.Opcode(), tokenizer.Data()) {
		err := fmt.Errorf("invalid Anchor tx script for txid")
		return nil, err
	}
	utxo := tokenizer.Data()

	// The Second opcode must be a canonical data push, the length of the
	// data push is bounded to 40 by the initial check on overall script
	// length.
	if !tokenizer.Next() ||
		!isCanonicalPush(tokenizer.Opcode(), tokenizer.Data()) {
		err := fmt.Errorf("invalid Anchor tx script for witnessScript")
		return nil, err
	}
	witnessScript := tokenizer.Data()

	// The third opcode must be a int64 ScriptNum.
	if !tokenizer.Next() {
		err := fmt.Errorf("invalid Anchor tx script for amount")
		return nil, err
	}
	value := extractScriptInt64(tokenizer.Data())

	// The Third opcode must be a canonical data push, The data is
	// serialized as assets
	if !tokenizer.Next() ||
		!isCanonicalPush(tokenizer.Opcode(), tokenizer.Data()) {
		err := fmt.Errorf("invalid Anchor tx script for witnessScript")
		return nil, err
	}
	assetsData := tokenizer.Data()

	txAssets := &wire.TxAssets{}
	if assetsData != nil {
		err := wire.DeserializeTxAssets(txAssets, assetsData)
		if err != nil {
			return nil, err
		}
	}

	// 读取sig
	if !tokenizer.Next() {
		return nil, fmt.Errorf("script too short: missing signature")
	}
	sig := tokenizer.Data()

	info := &AnchorInfo{
		Utxo:          string(utxo),
		WitnessScript: witnessScript,
		Value:         value,
		TxAssets:      txAssets,
		Sig:           sig,
	}
	return info, nil
}

// isCanonicalPush returns true if the opcode is either not a push instruction
// or the data associated with the push instruction uses the smallest
// instruction to do the job.  False otherwise.
//
// For example, it is possible to push a value of 1 to the stack as "OP_1",
// "OP_DATA_1 0x01", "OP_PUSHDATA1 0x01 0x01", and others, however, the first
// only takes a single byte, while the rest take more.  Only the first is
// considered canonical.
func isCanonicalPush(opcode byte, data []byte) bool {
	dataLen := len(data)
	if opcode > txscript.OP_16 {
		return true
	}

	if opcode < txscript.OP_PUSHDATA1 && opcode > txscript.OP_0 && (dataLen == 1 && data[0] <= 16) {
		return false
	}
	if opcode == txscript.OP_PUSHDATA1 && dataLen < txscript.OP_PUSHDATA1 {
		return false
	}
	if opcode == txscript.OP_PUSHDATA2 && dataLen <= 0xff {
		return false
	}
	if opcode == txscript.OP_PUSHDATA4 && dataLen <= 0xffff {
		return false
	}
	return true
}

// WitnessScriptHash generates a pay-to-witness-script-hash public key script
// paying to a version 0 witness program paying to the passed redeem script.
func WitnessScriptHash(witnessScript []byte) ([]byte, error) {
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(P2WSHSize),
	)

	bldr.AddOp(txscript.OP_0)
	scriptHash := sha256.Sum256(witnessScript)
	bldr.AddData(scriptHash[:])
	return bldr.Script()
}

// HexToSecp256k1PublicKey 将十六进制字符串格式的公钥转换为 secp256k1.PublicKey
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

func PublicKeyToTaprootAddress(pubKey *btcec.PublicKey) (*btcutil.AddressTaproot, error) {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	return btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), anchorManager.anchorConfig.ChainParams)
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

func CheckAnchorPkScript(anchorPkScript []byte) (*AscendInfo, error) {
	lockedTxInfo, err := ParseAnchorScript(anchorPkScript)
	if err != nil {
		return nil, err
	}

	pkScript, err := WitnessScriptHash(lockedTxInfo.WitnessScript)
	if err != nil {
		return nil, err
	}

	addrType, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, anchorManager.anchorConfig.ChainParams)
	if err != nil {
		return nil, err
	}
	if addrType != txscript.WitnessV0ScriptHashTy {
		return nil, fmt.Errorf("invalid addr type %d", addrType)
	}
	log.Infof("multi-signed address: %s", addresses[0].EncodeAddress()) // 多签地址，也就是L2锁定对应聪的地址

	addrType2, addresses2, _, err := txscript.ExtractPkScriptAddrs(lockedTxInfo.WitnessScript, anchorManager.anchorConfig.ChainParams)
	if err != nil {
		return nil, err
	}
	if addrType2 != txscript.MultiSigTy {
		return nil, fmt.Errorf("invalid addr type %d", addrType)
	}

	invoice, err := StandardAnchorScript(lockedTxInfo.Utxo, lockedTxInfo.WitnessScript,
		lockedTxInfo.Value, *lockedTxInfo.TxAssets)
	if err != nil {
		return nil, err
	}

	sig, err := ecdsa.ParseDERSignature(lockedTxInfo.Sig)
	if err != nil {
		return nil, err
	}

	if len(addresses2) != 2 {
		return nil, fmt.Errorf("invalid multi-sig addresses")
	}
	pubkeyBytes0 := addresses2[0].ScriptAddress()
	pubkeyBytes1 := addresses2[1].ScriptAddress()

	pubkeyA, err := BytesToPublicKey(pubkeyBytes0)
	if err != nil {
		return nil, fmt.Errorf("BytesToPublicKey failed. %v", err)
	}
	pubkeyB, err := BytesToPublicKey(pubkeyBytes1)
	if err != nil {
		return nil, fmt.Errorf("BytesToPublicKey failed. %v", err)
	}
	if !VerifyMessage(pubkeyA, invoice, sig) {
		if VerifyMessage(pubkeyB, invoice, sig) {
			pubkeyA, pubkeyB = pubkeyB, pubkeyA
			pubkeyBytes0, pubkeyBytes1 = pubkeyBytes1, pubkeyBytes0
		} else {
			return nil, fmt.Errorf("VerifyMessage failed")
		}
	}
	// A is server node, sign this invoice

	if !IsCoreNode(pubkeyBytes0) {
		return nil, fmt.Errorf("not signed by core node")
	}

	//utxoLocked := fmt.Sprintf("%s:%d", lockedTxInfo.TxId, lockedTxInfo.Index)
	lockedInfoInBTC, err := GetLockedUtxoInfo(lockedTxInfo.Utxo)
	if err != nil {
		return nil, err
	}
	if lockedInfoInBTC.Value != lockedTxInfo.Value {
		log.Debugf("lockedInfoInBTC.Amount: %d, lockedTxInfo.Amount: %d", lockedInfoInBTC.Value, lockedTxInfo.Value)
		return nil, fmt.Errorf("invalid value %d", lockedTxInfo.Value)
	}
	if !bytes.Equal(lockedInfoInBTC.pkScript, pkScript) {
		return nil, fmt.Errorf("invalid pkscript")
	}

	bindedValue := lockedTxInfo.TxAssets.GetBindingSatAmout()
	if bindedValue > lockedTxInfo.Value {
		log.Debugf("bindedValue: %d, lockedTxInfo.Amount: %d", bindedValue, lockedTxInfo.Value)
		return nil, fmt.Errorf("invalid binded value %d", bindedValue)
	}

	if !includeAssets(lockedInfoInBTC.AssetInfo, lockedTxInfo.TxAssets) {
		return nil, fmt.Errorf("invalid assets")
	}

	return &AscendInfo{
		AnchorInfo: *lockedTxInfo,
		Address:    addresses[0].EncodeAddress(),
		PubKeyA:    pubkeyBytes0,
		PubKeyB:    pubkeyBytes1,
	}, nil
}

func isSameAssets(utxoAssetInfo []*httpclient.UtxoAssetInfo, txAssets *wire.TxAssets) bool {
	if utxoAssetInfo == nil && txAssets == nil {
		return true
	}

	if utxoAssetInfo == nil || txAssets == nil {
		log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
		return false
	}

	if len(utxoAssetInfo) != len(*txAssets) {
		log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
		return false
	}

	for i := 0; i < len(utxoAssetInfo); i++ {
		if utxoAssetInfo[i].Asset.Name.Protocol != (*txAssets)[i].Name.Protocol {
			log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
			return false
		}
		if utxoAssetInfo[i].Asset.Name.Type != (*txAssets)[i].Name.Type {
			log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
			return false
		}
		if utxoAssetInfo[i].Asset.Name.Ticker != (*txAssets)[i].Name.Ticker {
			log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
			return false
		}
		if utxoAssetInfo[i].Asset.Amount.Cmp(&(*txAssets)[i].Amount) != 0 {
			log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
			return false
		}
		if utxoAssetInfo[i].Asset.BindingSat != (*txAssets)[i].BindingSat {
			log.Errorf("isSameAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
			return false
		}
	}

	return true
}
func includeAssets(utxoAssetInfo []*httpclient.UtxoAssetInfo, txAssets *wire.TxAssets) bool {
	if utxoAssetInfo == nil && txAssets == nil {
		return true
	}

	if utxoAssetInfo == nil && txAssets != nil {
		log.Errorf("includeAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
		return false
	}

	if len(utxoAssetInfo) < len(*txAssets) {
		log.Errorf("includeAssets failed, utxoAssetInfo: %v, txAssets: %v", utxoAssetInfo, txAssets)
		return false
	}

	for _, assetLocked := range *txAssets {
		assetFound := false
		for _, assetUtxo := range utxoAssetInfo {
			if isEqualAsset(assetUtxo, &assetLocked) {
				assetFound = true
				break
			}
		}
		if !assetFound {
			log.Errorf("includeAssets failed, locked Asset: %v not found in utxo", assetLocked)
			return false
		}
	}

	return true
}

func isEqualAsset(utxoAssetInfo *httpclient.UtxoAssetInfo, assetLocked *wire.AssetInfo) bool {
	if utxoAssetInfo.Asset.Name.Protocol != assetLocked.Name.Protocol {
		return false
	}
	if utxoAssetInfo.Asset.Name.Type != assetLocked.Name.Type {
		return false
	}
	if utxoAssetInfo.Asset.Name.Ticker != assetLocked.Name.Ticker {
		return false
	}
	if utxoAssetInfo.Asset.Amount.Cmp(&assetLocked.Amount) != 0 {
		return false
	}
	if utxoAssetInfo.Asset.BindingSat != assetLocked.BindingSat {
		return false
	}

	return true
}

// GenMultiSigScript generates the non-p2sh'd multisig script for 2 of 2
// pubkeys.
func GenMultiSigScript(aPub, bPub []byte) ([]byte, error) {
	if len(aPub) != 33 || len(bPub) != 33 {
		return nil, fmt.Errorf("pubkey size error: compressed " +
			"pubkeys only")
	}

	// Swap to sort pubkeys if needed. Keys are sorted in lexicographical
	// order. The signatures within the scriptSig must also adhere to the
	// order, ensuring that the signatures for each public key appears in
	// the proper order on the stack.
	if bytes.Compare(aPub, bPub) == 1 {
		aPub, bPub = bPub, aPub
	}

	// MultiSigSize 71 bytes
	//	- OP_2: 1 byte
	//	- OP_DATA: 1 byte (pubKeyAlice length)
	//	- pubKeyAlice: 33 bytes
	//	- OP_DATA: 1 byte (pubKeyBob length)
	//	- pubKeyBob: 33 bytes
	//	- OP_2: 1 byte
	//	- OP_CHECKMULTISIG: 1 byte
	MultiSigSize := 1 + 1 + 33 + 1 + 33 + 1 + 1
	bldr := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		MultiSigSize,
	))
	bldr.AddOp(txscript.OP_2)
	bldr.AddData(aPub) // Add both pubkeys (sorted).
	bldr.AddData(bPub)
	bldr.AddOp(txscript.OP_2)
	bldr.AddOp(txscript.OP_CHECKMULTISIG)
	return bldr.Script()
}

func GetP2WSHscript(a, b []byte) ([]byte, []byte, error) {
	// 根据闪电网络的规则，小的公钥放前面
	witnessScript, err := GenMultiSigScript(a, b)
	if err != nil {
		return nil, nil, err
	}

	pkScript, err := WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, nil, err
	}

	return witnessScript, pkScript, nil
}

func GetBTCAddressFromPkScript(pkScript []byte, chainParams *chaincfg.Params) (string, error) {
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, chainParams)
	if err != nil {
		return "", err
	}

	if len(addresses) == 0 {
		return "", fmt.Errorf("can't generate BTC address")
	}

	return addresses[0].EncodeAddress(), nil
}

func GetP2TRAddressFromPubkey(pubKey []byte, chainParams *chaincfg.Params) (string, error) {
	key, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return "", err
	}

	taprootPubKey := txscript.ComputeTaprootKeyNoScript(key)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), chainParams)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

func GetBootstrapPubKey() []byte {
	pubkey, _ := hex.DecodeString(common.BootstrapPubKey)
	return pubkey
}

func IsBootstrapPubKey(pubkey []byte) bool {
	return hex.EncodeToString(pubkey) == common.BootstrapPubKey
}

func GetCoreNodeChannelAddress(pubkey []byte, chainParams *chaincfg.Params) (string, error) {
	// 生成P2WSH地址
	_, pkScript, err := GetP2WSHscript(GetBootstrapPubKey(), pubkey)
	if err != nil {
		return "", err
	}

	// 生成地址
	address, err := GetBTCAddressFromPkScript(pkScript, chainParams)
	if err != nil {
		return "", err
	}

	return address, nil
}

// 包含bootstrap
func IsCoreNode(pubKey []byte) bool {
	if IsBootstrapPubKey(pubKey) {
		return true
	}

	if hex.EncodeToString(pubKey) == common.CoreNodePubKey {
		return true
	}

	// 从索引器查询结果：该节点已经与引导节点建立了通道，并且将资产质押到通道中（通过HasCoreNodeEligibility判断）
	return indexer.ShareIndexer.IsCoreNode(hex.EncodeToString(pubKey))
}
