package framework

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type PreviousOutputScriptResolver func(outpoint wire.OutPoint) ([]byte, bool)

func LastInputTaprootAddressResolver(moduleName string, params *chaincfg.Params) func(*wire.MsgTx) (string, error) {
	return func(tx *wire.MsgTx) (string, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", fmt.Errorf("missing %s invoker input", moduleName)
		}
		pubKey := ExtractAnyInputPublicKey(tx.TxIn[len(tx.TxIn)-1])
		if len(pubKey) == 0 {
			return "", fmt.Errorf("missing %s invoker public key", moduleName)
		}
		return TaprootAddressFromPubKey(pubKey, params)
	}
}

func LastInputContractActorResolver(moduleName string, params *chaincfg.Params) func(*wire.MsgTx, contract.Tx) (string, error) {
	resolve := LastInputTaprootAddressResolver(moduleName, params)
	return func(tx *wire.MsgTx, _ contract.Tx) (string, error) {
		return resolve(tx)
	}
}

func LastInputPreviousOutputAddressResolver(moduleName string, params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) func(*wire.MsgTx) (string, error) {

	return func(tx *wire.MsgTx) (string, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", fmt.Errorf("missing %s invoker input", moduleName)
		}
		if resolve != nil {
			outpoint := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
			if pkScript, ok := resolve(outpoint); ok {
				address, err := PreviousOutputAddress(pkScript, params)
				if err != nil {
					return "", err
				}
				if address != "" {
					return address, nil
				}
			}
		}
		return "", fmt.Errorf("missing %s invoker previous output address", moduleName)
	}
}

func LastInputPreviousOutputContractActorResolver(moduleName string, params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) func(*wire.MsgTx, contract.Tx) (string, error) {

	resolveAddress := LastInputPreviousOutputAddressResolver(moduleName, params, resolve)
	return func(tx *wire.MsgTx, _ contract.Tx) (string, error) {
		return resolveAddress(tx)
	}
}

func PreviousOutputAddress(pkScript []byte, params *chaincfg.Params) (string, error) {
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, ChainParamsOrTestNet(params))
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", nil
	}
	return addresses[0].EncodeAddress(), nil
}

func ChainParamsOrTestNet(params *chaincfg.Params) *chaincfg.Params {
	if params != nil {
		return params
	}
	return &chaincfg.TestNetParams
}

func TaprootAddressFromPubKey(pubKey []byte, params *chaincfg.Params) (string, error) {
	parsedPubKey, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return "", err
	}
	tapKey := txscript.ComputeTaprootKeyNoScript(parsedPubKey)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), ChainParamsOrTestNet(params))
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

func ExtractAnyInputPublicKey(txIn *wire.TxIn) []byte {
	if txIn == nil {
		return nil
	}
	if pubKey := findAnyPublicKeyPush(txIn.Witness); pubKey != nil {
		return CloneBytes(pubKey)
	}
	pushes, err := txscript.PushedData(txIn.SignatureScript)
	if err == nil {
		if pubKey := findAnyPublicKeyPush(pushes); pubKey != nil {
			return CloneBytes(pubKey)
		}
	}
	return nil
}

func findAnyPublicKeyPush(pushes [][]byte) []byte {
	for i := len(pushes) - 1; i >= 0; i-- {
		if len(pushes[i]) == 33 || len(pushes[i]) == 65 {
			return pushes[i]
		}
	}
	return nil
}
