package main

import (
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/btcwallet"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechain"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	DAEMON_INTERVAL = 30 * time.Second
	MAX_TIMES       = 3000 // MAX sync blocks number in one interval
)

type UtxoAssets struct {
	Utxo   string
	Value  int64
	Assets wire.TxAssets
}

var (
	syncedBlock   int64
	AddressAssets map[string]string
)

func startDaemon() {
	syncedBlock = -1
	AddressAssets = make(map[string]string)

	go func() {
		BlockMoniterThread()
	}()

	//syncBlock(31997)
}

func parseBlock(height int64) error {
	log.Debugf("-------------------  Block Info  -------------------------")
	log.Debugf("    Block: %d", height)
	hash, err := satsnet_rpc.GetBlockHash(height)
	if err != nil {
		log.Error(err)
		return err
	}

	log.Debugf("    Block Hash: %s", hash.String())
	block, err := satsnet_rpc.GetRawBlock(hash)
	if err != nil {
		log.Error(err)
		return err
	}

	// Show Block info
	log.Debugf("    Block Version: 0x%x", block.Header.Version)
	log.Debugf("    Prev Block Hash: %s", block.Header.PrevBlock.String())
	log.Debugf("    MerkleRoot Hash: %s", block.Header.MerkleRoot.String())
	log.Debugf("    Block Time: %s", block.Header.Timestamp.Format(time.DateTime))
	log.Debugf("    Block Bits: 0x%x", block.Header.Bits)
	log.Debugf("    Block Nonce: %d", block.Header.Nonce)
	log.Debugf("-------------------------  End  --------------------------")

	transactions := block.Transactions
	for index, tx := range transactions {

		log.Debugf("-------------------------------------------------------")
		log.Debugf("-------------------------------------------------------")
		log.Debugf("tx: %d", index)
		txid := tx.TxHash().String()
		log.Debugf("txid: %s ", txid)

		log.Debugf("-------------------------------------------------------")
		//txInBlock(tx)
		log.Debugf("------------TxIn-------------")
		for _, in := range tx.TxIn {
			txid := in.PreviousOutPoint.Hash.String()
			vout := in.PreviousOutPoint.Index
			utxo := fmt.Sprintf("%s:%d", txid, vout)
			SpentUtxo(utxo)
		}

		log.Debugf("------------TxOut-------------")

		for index, out := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", txid, index)

			assets := UtxoAssets{
				Utxo:   utxo,
				Value:  out.Value,
				Assets: out.Assets}

			NewUtxo(out.PkScript, assets)
		}
		log.Debugf("-------------------------------------------------------")
		log.Debugf("")
	}

	log.Debugf("Parse Block done.")
	return nil
}

func BlockMoniterThread() {
	// Create a ticker that ticks every n seconds
	ticker := time.NewTicker(DAEMON_INTERVAL)
	updatedCurrentHeight := int64(0)
	for {
		select {
		case <-ticker.C:
			log.Debugf("Moniter timeup...")

			// Check the day is changed
			// today := time.Now().Format(time.DateOnly)

			// recordToday = moniterInstance.marketPlaceInst.GetRecordToday()

			// if recordToday != today {
			// 	moniterInstance.marketPlaceInst.TodayPast()
			// }

			// moniterInstance.marketPlaceInst.UpdateBTCPrice()

			currentHeight, err := satsnet_rpc.GetBlockCount()
			if err != nil {
				continue
			}
			// if syncedBlock == 0 {
			// 	syncedBlock = currentHeight
			// }

			if updatedCurrentHeight != currentHeight {
				// The current height is updated
				//moniterInstance.marketPlaceInst.CurrentHeightUpdated(currentHeight)
				updatedCurrentHeight = currentHeight

			}
			blocks := 0

			for {
				blocks++
				// Check the block height of btc is changed
				log.Debugf("************************************************************************")
				log.Debugf("************************************************************************")
				log.Debugf("current Block: %d, Synced Block: %d", currentHeight, syncedBlock)
				if currentHeight <= syncedBlock {
					// no new block
					break
				}
				syncingBlock := syncedBlock + 1
				err = parseBlock(syncingBlock)
				if err != nil {
					log.Errorf("UtxoMoniterThread->syncBlock failed: %s", err)
					break
				}
				syncedBlock = syncingBlock
				log.Debugf("Has Synced Block: %d", syncedBlock)
				if blocks > MAX_TIMES {
					// max check 10 blocks in one time
					break
				}
				log.Debugf("************************************************************************")
				log.Debugf("")
			}
		}
	}
}

func SpentUtxo(utxo string) {
	log.Debugf("utxo: %s", utxo)
	log.Debugf("----------------------------------")
}

func NewUtxo(pkScript []byte, assets UtxoAssets) {
	address, err := btcwallet.PkScriptToAddr(pkScript)
	log.Debugf("pkScript: %x", pkScript)
	if err != nil {
		log.Errorf("NewUtxo->PkScriptToAddr failed: %v ", err)
	} else {
		log.Debugf("address: %s", address)
	}
	log.Debugf("utxo:%s, Value:%d", assets.Utxo, assets.Value)
	log.Debugf("TxAssets count: %d", len(assets.Assets))
	for index, asset := range assets.Assets {
		log.Debugf("---------------------------------")
		log.Debugf("	TxAssets index: %d", index)
		log.Debugf("	TxAssets Name Protocol: %s", asset.Name.Protocol)
		log.Debugf("	TxAssets Name Type: %s", asset.Name.Type)
		log.Debugf("	TxAssets Name Ticker: %s", asset.Name.Ticker)
		log.Debugf("	TxAssets Amount: %s", asset.Amount.String())
		log.Debugf("	TxAssets BindingSat: %d", asset.BindingSat)
	}
	log.Debugf("----------------------------------")
}

func ShowBlocks(start, end int64) {
	currentHeight, err := satsnet_rpc.GetBlockCount()
	if err != nil {
		return
	}

	if start < 0 {
		start = 0
	}

	if end == -1 {
		end = currentHeight
	}

	for block := start; block <= end; block++ {
		// Check the block height of btc is changed
		log.Debugf("************************************************************************")
		log.Debugf("************************************************************************")
		log.Debugf("Block height: %d,  Shows Block height: %d", currentHeight, block)
		if block > currentHeight {
			// no more block
			break
		}
		err = parseBlock(block)
		if err != nil {
			log.Errorf("parseBlock failed: %s", err)
			break
		}
		//log.Debugf("Show Block: %d completed.", block)
		log.Debugf("************************************************************************")
		log.Debugf("")
	}
}

func ShowVCBlocks(start, end int64) {
	vcBlockState, err := satsnet_rpc.GetVCBlockState()
	if err != nil {
		log.Errorf("GetVCBlockState failed: %s", err)
		return
	}

	if start < 0 {
		start = 0
	}

	if end == -1 {
		end = vcBlockState.Height
	}

	for blockHeight := start; blockHeight <= end; blockHeight++ {
		// Check the block height of btc is changed
		log.Debugf("************************************************************************")
		log.Debugf("************************************************************************")
		log.Debugf("VCBlock height: %d,  Shows VC Block height: %d", vcBlockState.Height, blockHeight)
		if blockHeight > vcBlockState.Height {
			// no more block
			break
		}
		err = parseVCBlock(blockHeight)
		if err != nil {
			log.Errorf("Block %d parseBlock failed: %s", blockHeight, err)
			continue
		}
		//log.Debugf("Show Block: %d completed.", block)
		log.Debugf("************************************************************************")
		log.Debugf("")
	}
}

func TestRPCGetBlocks() {

	times := 10000
	failedTimes := 0
	for i := 0; i <= times; i++ {
		currentHeight, err := satsnet_rpc.GetBlockCount()
		if err != nil {
			log.Error("GetBlockCount failed: ", err)
			failedTimes++
			continue
		}

		_, err = satsnet_rpc.GetBlockHash(currentHeight)
		if err != nil {
			log.Error("GetBlockHash failed: ", err)
			failedTimes++
			continue
		}
	}

	log.Debugf("TestBlocks times: %d, failed times: %d", times, failedTimes)

}

func GetDataType(dataType uint32) string {
	switch dataType {
	case validatechain.DataType_NewEpoch:
		return "New Epoch"

	case validatechain.DataType_UpdateEpoch:
		return "Update Epoch"

	case validatechain.DataType_GeneratorHandOver:
		return "Generator Hand Over"

	case validatechain.DataType_MinerNewBlock:
		return "Miner New Block"

	default:
		return "Unknow data type"
	}
}

func parseVCBlock(height int64) error {
	log.Debugf("-------------------  VC Block Info  -------------------------")
	log.Debugf("    Block: %d", height)
	vsBlockHash, err := satsnet_rpc.GetVCBlockHash(height)
	if err != nil {
		log.Error(err)
		return err
	}

	log.Debugf("    Block Hash: %s", vsBlockHash.Hash)
	hash, err := chainhash.NewHashFromStr(vsBlockHash.Hash)
	if err != nil {
		log.Error(err)
		return err
	}

	blockResult, err := satsnet_rpc.GetVCBlock(hash)
	if err != nil {
		log.Error(err)
		return err
	}

	blockData := blockResult.DataPayload
	vcBlock := validatechain.VCBlock{}
	err = vcBlock.Decode(blockData)
	if err != nil {
		log.Error(err)
		return err
	}
	// Show Block info
	log.Debugf("    Block Version: %d", vcBlock.Header.Version)
	log.Debugf("    Prev Block Hash: %s", vcBlock.Header.PrevHash.String())
	log.Debugf("    Block Time: %s", time.Unix(vcBlock.Header.CreateTime, 0).Format(time.DateTime))
	log.Debugf("    Block Data type: %s", GetDataType(vcBlock.Header.DataType))

	log.Debugf("-------------------------  End  --------------------------")
	switch vcd := vcBlock.Data.(type) { //nolint:gocritice := vcd.Data.(type)
	case *validatechain.DataNewEpoch:
		showNewEpochData(vcd)
	case *validatechain.DataUpdateEpoch:
		showUpdateEpochData(vcd)
	case *validatechain.DataGeneratorHandOver:
		showGeneratorHandOverData(vcd)
	case *validatechain.DataMinerNewBlock:
		showMinerNewBlockData(vcd)
	case *validatechain.DataEpochDelMember:
		showDelEpochMemberData(vcd)
	}

	log.Debugf("Parse VC Block done.")
	return nil
}

var (
	NewEpochReason = map[uint32]string{
		validatechain.NewEpochReason_EpochCreate:   "Epoch init",
		validatechain.NewEpochReason_EpochHandOver: "Epoch handover",
		validatechain.NewEpochReason_EpochStopped:  "Epoch stopped",
	}

	UpdateEpochReason = map[uint32]string{
		validatechain.UpdateEpochReason_EpochHandOver:     "Epoch handover",
		validatechain.UpdateEpochReason_GeneratorHandOver: " Generator handover",
		validatechain.UpdateEpochReason_MemberRemoved:     "Member removed",
	}
)

func showNewEpochData(newEpochData *validatechain.DataNewEpoch) {
	log.Debugf("-------------------     New Epoch Data   -------------------------")
	log.Debugf("    EpochIndex: %d", newEpochData.EpochIndex)
	log.Debugf("    Creator ID: %s", newEpochData.CreatorId)
	log.Debugf("    CreateTime: %s", time.Unix(newEpochData.CreateTime, 0).Format(time.DateTime))
	log.Debugf("    Create Reason: %s", NewEpochReason[newEpochData.Reason])

	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------    Epoch list   -----------------")
	log.Debugf("-------------------------------------------------")
	for index, epochItem := range newEpochData.EpochItemList {
		log.Debugf("    Index: %d", index)
		log.Debugf("    Validator ID: %s", epochItem.ValidatorId)
		log.Debugf("-------------------------------------------------")
	}

	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------    Vote list   -----------------")
	log.Debugf("-------------------------------------------------")
	for index, epochItem := range newEpochData.EpochVoteList {
		log.Debugf("    Index: %d", index)
		log.Debugf("    Validator ID: %d", epochItem.ValidatorId)
		log.Debugf("    Hash: %x", epochItem.Hash.String())
		log.Debugf("-------------------------------------------------")
	}
	log.Debugf("-------------------  New Epoch Data End  -------------------------")
}

func showUpdateEpochData(updateEpochData *validatechain.DataUpdateEpoch) {
	log.Debugf("-------------------     Update Epoch Data   -------------------------")
	log.Debugf("    UpdatedId: %s", updateEpochData.UpdatedId)
	log.Debugf("    EpochIndex: %d", updateEpochData.EpochIndex)
	log.Debugf("    CreateTime: %s", time.Unix(updateEpochData.CreateTime, 0).Format(time.DateTime))
	log.Debugf("    Reason: %s", UpdateEpochReason[updateEpochData.Reason])
	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------    Epoch list   -----------------")
	log.Debugf("-------------------------------------------------")
	for index, epochItem := range updateEpochData.EpochItemList {
		log.Debugf("    Index: %d", index)
		log.Debugf("    Validator ID: %s", epochItem.ValidatorId)
		log.Debugf("-------------------------------------------------")
	}

	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------    Generator   -----------------")
	log.Debugf("-------------------------------------------------")
	log.Debugf("    GeneratorPos: %d", updateEpochData.GeneratorPos)
	log.Debugf("    GeneratorId: %d", updateEpochData.Generator.GeneratorId)
	log.Debugf("    Generator Height: %d", updateEpochData.Generator.Height)
	log.Debugf("    Generator Timestamp: %s", time.Unix(updateEpochData.Generator.Timestamp, 0).Format(time.DateTime))
	log.Debugf("    Generator Token: %s", updateEpochData.Generator.Token)
	log.Debugf("-------------------------------------------------")
	log.Debugf("-------------------  Update Epoch Data End  -------------------------")
}

func showDelEpochMemberData(delEpochMemberData *validatechain.DataEpochDelMember) {
	log.Debugf("-------------------     Del Epoch member Data   -------------------------")
	log.Debugf("    RequestId: %s", delEpochMemberData.RequestId)
	log.Debugf("    EpochIndex: %d", delEpochMemberData.EpochIndex)
	log.Debugf("    CreateTime: %s", time.Unix(delEpochMemberData.CreateTime, 0).Format(time.DateTime))
	log.Debugf("    Reason: %s", UpdateEpochReason[delEpochMemberData.Reason])
	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------  Epoch list after member deleteed  -----------------")
	log.Debugf("-------------------------------------------------")
	for index, epochItem := range delEpochMemberData.EpochItemList {
		log.Debugf("    Index: %d", index)
		log.Debugf("    Validator ID: %s", epochItem.ValidatorId)
		log.Debugf("-------------------------------------------------")
	}

	log.Debugf("-------------------------------------------------")
	log.Debugf("---------------    confirmItem list   -----------------")
	log.Debugf("-------------------------------------------------")
	for index, confirmItem := range delEpochMemberData.EpochDelConfirmList {
		log.Debugf("    Index: %d", index)
		log.Debugf("    Validator ID: %s", confirmItem.ValidatorId)
		log.Debugf("    Result: %d", confirmItem.Result)
		log.Debugf("    Token: %s", confirmItem.Token)
		log.Debugf("-------------------------------------------------")
	}

	log.Debugf("-------------------  Update Epoch Data End  -------------------------")
}

func showGeneratorHandOverData(generatorHandOverData *validatechain.DataGeneratorHandOver) {
	log.Debugf("-------------------     Generator HandOver Data   -------------------------")
	log.Debugf("-------------------  Generator HandOver Data End  -------------------------")
}

func showMinerNewBlockData(newBlockData *validatechain.DataMinerNewBlock) {
	log.Debugf("-------------------    Miner New Block Data   -------------------------")
	log.Debugf("    GeneratorId: %s", newBlockData.GeneratorId)
	log.Debugf("    Timestamp: %s", time.Unix(newBlockData.Timestamp, 0).Format(time.DateTime))
	log.Debugf("    SatsnetHeight: %d", newBlockData.SatsnetHeight)
	log.Debugf("    Hash: %s", newBlockData.Hash.String())
	log.Debugf("    Token: %s", newBlockData.Token)
	log.Debugf("-------------------  Miner New Block Data End  -------------------------")
}
