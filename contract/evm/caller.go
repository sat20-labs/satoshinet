package evm

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type PreviousOutputScriptResolver func(outpoint wire.OutPoint) ([]byte, bool)

func EVMAddressFromPublicKey(pubKey []byte) (EVMAddress, error) {
	var addr EVMAddress
	if !isSupportedPublicKey(pubKey) {
		return addr, fmt.Errorf("unsupported public key length %d", len(pubKey))
	}
	hash := btcutil.Hash160(pubKey)
	copy(addr[:], hash)
	return addr, nil
}

func LastInputCallerResolver(tx *wire.MsgTx, parsed ParsedTx) (EVMAddress, error) {
	var zero EVMAddress
	if tx == nil {
		return zero, errors.New("missing transaction")
	}
	if len(tx.TxIn) == 0 {
		return zero, errors.New("transaction has no inputs")
	}
	pubKey, err := ExtractInputPublicKey(tx.TxIn[len(tx.TxIn)-1])
	if err != nil {
		return zero, err
	}
	return EVMAddressFromPublicKey(pubKey)
}

func LastInputPreviousOutputCallerResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) CallerResolver {

	return func(tx *wire.MsgTx, parsed ParsedTx) (EVMAddress, error) {
		var zero EVMAddress
		if tx == nil {
			return zero, errors.New("missing transaction")
		}
		if len(tx.TxIn) == 0 {
			return zero, errors.New("transaction has no inputs")
		}
		if resolve != nil {
			outpoint := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
			if pkScript, ok := resolve(outpoint); ok {
				_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, callerChainParams(params))
				if err != nil {
					return zero, err
				}
				if len(addresses) != 0 {
					hash := btcutil.Hash160([]byte(addresses[0].EncodeAddress()))
					copy(zero[:], hash)
					return zero, nil
				}
			}
		}
		return zero, errors.New("missing caller previous output address")
	}
}

func callerChainParams(params *chaincfg.Params) *chaincfg.Params {
	if params != nil {
		return params
	}
	return &chaincfg.TestNetParams
}

func ExtractInputPublicKey(txIn *wire.TxIn) ([]byte, error) {
	if txIn == nil {
		return nil, errors.New("missing transaction input")
	}
	if pubKey := findPublicKeyPush(txIn.Witness); pubKey != nil {
		return cloneBytes(pubKey), nil
	}
	pushes, err := txscript.PushedData(txIn.SignatureScript)
	if err != nil {
		return nil, err
	}
	if pubKey := findPublicKeyPush(pushes); pubKey != nil {
		return cloneBytes(pubKey), nil
	}
	return nil, errors.New("input does not reveal a supported public key")
}

func findPublicKeyPush(pushes [][]byte) []byte {
	for i := len(pushes) - 1; i >= 0; i-- {
		if isSupportedPublicKey(pushes[i]) {
			return pushes[i]
		}
	}
	return nil
}

func isSupportedPublicKey(pubKey []byte) bool {
	switch len(pubKey) {
	case 33:
		return pubKey[0] == 0x02 || pubKey[0] == 0x03
	case 65:
		return pubKey[0] == 0x04 || pubKey[0] == 0x06 || pubKey[0] == 0x07
	default:
		return false
	}
}
