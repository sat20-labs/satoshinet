package main

import (
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/btcwallet"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/satsnet_rpc"
)

func testGetBlockStats(height int, stats []string) {
	fmt.Printf("test GetBlockStats...\n")

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.GetBlockStats(height, &stats)
	if err != nil {
		fmt.Printf("GetBlockStats error: %s\n", err)
		return
	}
	fmt.Printf("GetBlockStats success,result: %v\n", result)

}

func testGetMempoolEntry(txid string) {
	fmt.Printf("test GetMempoolEntry...\n")

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.GetMempoolEntry(txid)
	if err != nil {
		fmt.Printf("GetMempoolEntry error: %s\n", err)
		return
	}
	fmt.Printf("GetMempoolEntry success,result: %v\n", result)

}

func testGetRawTransaction(txid string) {
	fmt.Printf("test getrawtransaction...\n")

	txHash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		log.Errorf("Invalid Txid : %s", txid)
	}

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.GetRawTransaction(txHash)
	if err != nil {
		fmt.Printf("getrawtransaction error: %s\n", err)
		return
	}

	btcwallet.LogMsgTx(result.MsgTx())
	fmt.Printf("getrawtransaction success,result: %v\n", result)

}

func testSendRawTransaction(rawSend string) {
	fmt.Printf("test sendrawtransaction...\n")

	raw := rawSend
	if raw == "" {
		raw = "01000000000102553ce5be0f7e3c2971a4e14d76fd15101760f9cce4122a2ae22f4c0d73a98a620000000000ffffffff553ce5be0f7e3c2971a4e14d76fd15101760f9cce4122a2ae22f4c0d73a98a620100000000ffffffff02194800000000000001ff83134c0265790000fd1948220020650c7b012cd5aa9201251bb1bedefc62aa84548a73bb259595959029e20116160a0000000000000001ff9c5b4c02657900000a2251208be89118321c458463a3f3404d626559dc4adeccb4017846e920b32d32e374d9040048304502210088895b506a9cee8b90180af66007b196c77b4ae00db3c2b4725b4706d662755302207843bab10206371d69780a18dbad9bfe883276c2b9f4d28d937926096303b16d01483045022100fae920f172165d503bcd99cbda817ea0a433409cbae77c097c2e51a04b7989da02204f06e0fb15a275355fc4a4d31583187d158a4f8a34dd8c57f73687004dff40eb014752210304374824804565034f8a7ffb680f05a20db8ef6888d5621d8577f6f412596f9e21036fa703396bdcbf4b3466c62079df08366b080e9832f28fd246c0eeea90be40d052ae01405f989f76dafc2b53ddd239a35ff52f133a421a35a648fc747b05a819c6ddcfee40bf746d871bdd1cea5dc10aa088b65055b3fcf43f62884851a514bee53a12de00000000"
	}

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.SendRawTransaction(raw, true)
	if err != nil {
		fmt.Printf("sendrawtransaction error: %s\n", err)
		return
	}

	//btcwallet.LogMsgTx(result.MsgTx())
	fmt.Printf("sendrawtransaction success,result: %v\n", result)

}

func testGetAccordingLockedInfo(txid string) {
	fmt.Printf("testGetAccordingLockedInfo...\n")

	// Get Anchor tx from sats net
	txHash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		log.Errorf("Invalid Txid : %s", txid)
	}

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.GetRawTransaction(txHash)
	if err != nil {
		fmt.Printf("getrawtransaction error: %s\n", err)
		return
	}

	//btcwallet.LogMsgTx(result.MsgTx())

	lockedTxInfo, err := anchortx.GetLockedTxInfo(result.MsgTx(), false)
	if err != nil {
		fmt.Printf("GetLockedTxInfo error: %s\n", err)
		return
	}

	fmt.Printf("Locked TX info: \n")
	fmt.Printf("Locked TX txid(BTC): %s\n", lockedTxInfo.Utxo)
	fmt.Printf("Locked TX WitnessScript: %v\n", lockedTxInfo.WitnessScript)
	fmt.Printf("Locked TX Amount: %d\n", lockedTxInfo.Value)

	fmt.Printf("Anchor TX txid(Satsnet): %s\n", txid)
}

func testGetAccordingAnchorInfo(lockedTxid string) {
	fmt.Printf("testGetAccordingAnchorInfo...\n")

	// SendRawTransaction(raw)
	result, err := satsnet_rpc.GetAnchorTxInfo(lockedTxid)
	if err != nil {
		fmt.Printf("testGetAccordingAnchorInfo error: %s\n", err)
		return
	}

	//btcwallet.LogMsgTx(result.MsgTx())

	fmt.Printf("According TX info: \n")
	fmt.Printf("Locked TX Utxo(BTC): %s\n", result.LockedUtxo)
	fmt.Printf("Locked TX Witness: %s\n", result.Witness)
	fmt.Printf("Locked TX Amount: %d\n", result.Amount)

	fmt.Printf("Anchor TX txid(Satsnet): %s\n", result.AnchorTxid)

	fmt.Printf("************************************Anchor TX Info**************************************\n")
	// Get Anchor tx from sats net
	txHash, err := chainhash.NewHashFromStr(result.AnchorTxid)
	if err != nil {
		log.Errorf("Invalid Anchor Txid : %s", result.AnchorTxid)
		return
	}

	// SendRawTransaction(raw)
	resultTx, err := satsnet_rpc.GetRawTransaction(txHash)
	if err != nil {
		fmt.Printf("getrawtransaction error: %s\n", err)
		return
	}

	btcwallet.LogMsgTx(resultTx.MsgTx())
	fmt.Printf("************************************Anchor TX End**************************************\n")
}

func testGetBlocksWithTx(txid string) {
	tx, err := satsnet_rpc.GetTxVerbose(txid)
	if err != nil {
		log.Errorf("Invalid Txid : %s", txid)
		return
	}
	log.Infof("Tx Id: %s", tx.Txid)
	log.Infof("Tx Hex: %s", tx.Hex)
	log.Infof("Tx Hash: %s", tx.Hash)
	log.Infof("Block: %s", tx.BlockHash)

	block, err := satsnet_rpc.GetRawBlockVerbose(tx.BlockHash)
	if err != nil {
		log.Errorf("Invalid Block : %s", tx.BlockHash)
		return
	}

	log.Infof("Block Height: %d", block.Height)
	log.Infof("Block Hash: %d", block.Hash)
	log.Infof("Block Time: %s", time.Unix(block.Time, 0).Format(time.DateTime)) //time.Unix(block.Time.Format(time.DateTime))
}
