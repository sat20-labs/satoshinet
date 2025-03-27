// Copyright (c) 2024 The sats20 developers
// All Locked tx info from BTC chain, not satsnet

package anchortx

import (
	"fmt"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/satoshinet/httpclient"
)

type LockedInfoInBTCChain struct {
	Utxo string // the txid with locked in lnd
	//LockedAddresses []string // the locked addresses, it should be multi sig address
	pkScript  []byte // the pkscript
	Value     int64  // the sats with locked in lnd
	AssetInfo []*httpclient.UtxoAssetInfo
}

func GetLockedUtxoInfo(utxo string) (*LockedInfoInBTCChain, error) {
	lockedInfo := &LockedInfoInBTCChain{}
	scheme := anchorManager.anchorConfig.IndexerScheme
	host := anchorManager.anchorConfig.IndexerHost
	proxy := anchorManager.anchorConfig.IndexerProxy

	// Get TxInfo from BTC chain (Layer 1 chain)
	indexerClient := httpclient.NewIndexerClient(scheme, host, proxy)
	utxoAssetsInfo, err := indexerClient.GetTxUtxoAssets(utxo)
	if err != nil {
		fmt.Printf("GetRawTx failed: %s\n", err.Error())
		return nil, err
	}

	lockedInfo.Utxo = utxo
	lockedInfo.pkScript = utxoAssetsInfo.OutValue.PkScript
	lockedInfo.Value = utxoAssetsInfo.OutValue.Value
	lockedInfo.AssetInfo = utxoAssetsInfo.AssetInfo

	return lockedInfo, nil
}

func logMsgTx(tx *wire.MsgTx) {
	logLine("tx:%s", tx.TxHash().String())
	logLine("txin: %d", len(tx.TxIn))
	for index, txin := range tx.TxIn {
		logLine("		txin index: %d", index)
		logLine("		txin utxo txid: %s", txin.PreviousOutPoint.Hash.String())
		logLine("		txin utxo index: %d", txin.PreviousOutPoint.Index)
		logLine("		txin utxo Wintness: ")
		logLine("		{")
		for _, witness := range txin.Witness {
			logLine("		%x", witness)
		}
		logLine("		}")
		logLine("		txin SignatureScript: %x", txin.SignatureScript)
		logLine("		---------------------------------")
	}

	logLine("txout: %d", len(tx.TxOut))
	for index, txout := range tx.TxOut {
		logLine("		txout index: %d", index)
		logLine("		txout pkscript: %x", txout.PkScript)

		if txscript.IsNullData(txout.PkScript) {
			logLine("		txout pkscript is an OP_RETURN script")
		} else {
			addr, err := pkScriptToAddr(txout.PkScript)
			if err != nil {
				logLine("		txout pkscript is an invalidaddress: %s", err)
			} else {
				logLine("		txout address: %s", addr)
			}
		}
		logLine("		txout value: %d", txout.Value)
		logLine("		---------------------------------")
	}

}

func logLine(format string, a ...any) {
	logStr := fmt.Sprintf(format, a...)
	fmt.Println(logStr)
}

func pkScriptToAddr(pkScript []byte) (string, error) {

	if len(pkScript) > 0 && pkScript[0] == txscript.OP_RETURN {
		err := fmt.Errorf("pkscript is OP_RETURN Script")
		return "", err
	}

	var NetParams = chaincfg.MainNetParams
	if anchorManager.anchorConfig.ChainParams.Name != "satsnet" {
		NetParams = chaincfg.TestNet4Params
	}
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, &NetParams)
	if err != nil {
		return "", err
	}

	if len(addrs) == 0 {
		err := fmt.Errorf("failed to get addr with pkscript[%v]", pkScript)
		return "", err
	}

	return addrs[0].EncodeAddress(), nil

}
