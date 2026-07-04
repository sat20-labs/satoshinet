package dkvs

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
)

func P2TRAddressFromPubKeyBytes(pubKey []byte, params *chaincfg.Params) (string, error) {
	if params == nil {
		params = &chaincfg.MainNetParams
	}
	key, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return "", err
	}
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(key)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), params)
	if err != nil {
		return "", err
	}
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, params)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("can't generate p2tr address")
	}
	return addresses[0].EncodeAddress(), nil
}
