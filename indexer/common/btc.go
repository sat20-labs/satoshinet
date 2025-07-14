package common

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

var (
	ChainTestnet = chaincfg.TestNetParams.Name
	ChainMainnet = chaincfg.MainNetParams.Name
)

func PkScriptToAddr(pkScript []byte, chain string) (string, error) {
	chainParams := &chaincfg.TestNetParams
	switch chain {
	case ChainTestnet:
		chainParams = &chaincfg.TestNetParams
	case ChainMainnet:
		chainParams = &chaincfg.MainNetParams
	}
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, chainParams)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("no address")
	}
	return addrs[0].EncodeAddress(), nil
}

func IsValidAddr(addr string, chain string) (bool, error) {
	chainParams := &chaincfg.TestNetParams
	switch chain {
	case ChainTestnet:
		chainParams = &chaincfg.TestNetParams
	case ChainMainnet:
		chainParams = &chaincfg.MainNetParams
	default:
		return false, nil
	}
	_, err := btcutil.DecodeAddress(addr, chainParams)
	if err != nil {
		return false, err
	}
	return true, nil
}

func AddrToPkScript(addr string, chain string) ([]byte, error) {
	chainParams := &chaincfg.MainNetParams
	switch chain {
	case ChainTestnet:
		chainParams = &chaincfg.TestNetParams
	case ChainMainnet:
		chainParams = &chaincfg.MainNetParams
	default:
		return nil, fmt.Errorf("invalid chain: %s", chain)
	}
	address, err := btcutil.DecodeAddress(addr, chainParams)
	if err != nil {
		return nil, err
	}
	return txscript.PayToAddrScript(address)
}

func IsOpReturn(pkScript []byte) bool {
	if len(pkScript) < 1 || pkScript[0] != txscript.OP_RETURN {
		return false
	}

	// Single OP_RETURN.
	if len(pkScript) == 1 {
		return true
	}
	if len(pkScript) > txscript.MaxDataCarrierSize {
		return false
	}

	return true
}

func IsCoinbaseTx(tx *wire.MsgTx) bool {
	// A coinbase transaction must have exactly one input.
	if len(tx.TxIn) != 1 {
		return false
	}

	// Check if the input's previous outpoint hash is all zeros and index is 0xFFFFFFFF.
	prevOut := tx.TxIn[0].PreviousOutPoint
	zeroHash := [32]byte{}
	if !bytes.Equal(prevOut.Hash[:], zeroHash[:]) || prevOut.Index != wire.MaxTxInSequenceNum {
		return false
	}

	// If the above conditions are met, it's a coinbase transaction.
	return true
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

func GetChannelAddress(pubkeyA, pubkeyB []byte, chainParams *chaincfg.Params) (string, error) {
	// 生成P2WSH地址
	_, pkScript, err := indexer.GetP2WSHscript(pubkeyA, pubkeyB)
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


func GetDefaultChannelAddress(chainParams *chaincfg.Params) (string, error) {
	// 生成P2WSH地址
	bootstrappubkey, _ := hex.DecodeString(indexer.GetBootstrapPubKey())
	corenodepubkey, _ := hex.DecodeString(indexer.GetCoreNodePubKey())
	return GetChannelAddress(bootstrappubkey, corenodepubkey, chainParams)
}
