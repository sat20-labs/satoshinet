package agent

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type PreviousOutputScriptResolver func(outpoint wire.OutPoint) ([]byte, bool)

func LastInputInvokerResolver(params *chaincfg.Params) InvokerResolver {
	return func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", fmt.Errorf("missing agent invoker input")
		}
		script := tx.TxIn[len(tx.TxIn)-1].SignatureScript
		if len(script) == 0 {
			pubKey := extractInvokerPubKeyFromWitness(tx.TxIn[len(tx.TxIn)-1].Witness)
			if len(pubKey) == 0 {
				return "", fmt.Errorf("missing agent invoker public key")
			}
			return taprootAddressFromPubKey(pubKey, params)
		}
		pubKey := extractInvokerPubKey(script)
		if len(pubKey) == 0 {
			return "", fmt.Errorf("missing agent invoker public key")
		}
		return taprootAddressFromPubKey(pubKey, params)
	}
}

func LastInputPreviousOutputInvokerResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) InvokerResolver {

	return func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", fmt.Errorf("missing agent invoker input")
		}
		if resolve != nil {
			outpoint := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
			if pkScript, ok := resolve(outpoint); ok {
				_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, invokerChainParams(params))
				if err != nil {
					return "", err
				}
				if len(addresses) != 0 {
					return addresses[0].EncodeAddress(), nil
				}
			}
		}
		return "", fmt.Errorf("missing agent invoker previous output address")
	}
}

func invokerChainParams(params *chaincfg.Params) *chaincfg.Params {
	if params != nil {
		return params
	}
	return &chaincfg.TestNetParams
}

func taprootAddressFromPubKey(pubKey []byte, params *chaincfg.Params) (string, error) {
	parsedPubKey, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return "", err
	}
	tapKey := txscript.ComputeTaprootKeyNoScript(parsedPubKey)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), params)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

func extractInvokerPubKey(script []byte) []byte {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	for tokenizer.Next() {
		data := tokenizer.Data()
		if len(data) == 33 || len(data) == 65 {
			return data
		}
	}
	return nil
}

func extractInvokerPubKeyFromWitness(witness wire.TxWitness) []byte {
	for _, data := range witness {
		if len(data) == 33 || len(data) == 65 {
			return data
		}
	}
	return nil
}
