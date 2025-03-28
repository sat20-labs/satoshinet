package base

import (
	"encoding/hex"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// 不要panic，可能会影响写数据库
func (b *BaseIndexer) fetchBlock(height int) *common.Block {
	hash, err := getBlockHash(uint64(height))
	if err != nil {
		common.Log.Errorf("getBlockHash %d failed. %v", height, err)
		return nil
		//common.Log.Fatalln(err)
	}

	block, err := getRawBlock(hash)
	if err != nil {
		common.Log.Errorf("getRawBlock %d %s failed. %v", height, hash, err)
		return nil
		//common.Log.Fatalln(err)
	}

	return ConvertBlock(block, height, b.chaincfgParam)
}

func ConvertBlock(block *wire.MsgBlock, height int, chaincfgParam *chaincfg.Params) *common.Block {
	transactions := block.Transactions
	txs := make([]*common.Transaction, len(transactions))
	for i, tx := range transactions {
		inputs := []*common.Input{}
		outputs := []*common.Output{}

		for _, v := range tx.TxIn {
			txid := v.PreviousOutPoint.Hash.String()
			vout := v.PreviousOutPoint.Index
			input := &common.Input{Txid: txid, Vout: (vout), Witness: v.Witness, SignatureScript: v.SignatureScript}
			inputs = append(inputs, input)
		}

		// parse the raw tx values
		for j, v := range tx.TxOut {
			// Determine the type of the script and extract the address
			scyptClass, addrs, reqSig, err := txscript.ExtractPkScriptAddrs(v.PkScript, chaincfgParam)
			if err != nil {
				common.Log.Errorf("ExtractPkScriptAddrs %d failed. %v", height, err)
				return nil
				//common.Log.Panicf("BaseIndexer.fetchBlock-> Failed to extract address: %v", err)
			}

			addrsString := make([]string, len(addrs))
			for i, x := range addrs {
				if scyptClass == txscript.MultiSigTy {
					addrsString[i] = hex.EncodeToString(x.ScriptAddress()) // pubkey
				} else {
					addrsString[i] = x.EncodeAddress()
				}
			}

			var receiver common.ScriptPubKey

			if len(addrs) == 0 {
				address := "UNKNOWN"
				if scyptClass == txscript.NullDataTy {
					address = "OP_RETURN"
				}
				receiver = common.ScriptPubKey{
					Addresses: []string{address},
					Type:      int(scyptClass),
					PkScript:  v.PkScript,
					ReqSig:    reqSig,
				}
			} else {
				receiver = common.ScriptPubKey{
					Addresses: addrsString,
					Type:      int(scyptClass),
					PkScript:  v.PkScript,
					ReqSig:    reqSig,
				}
			}

			output := &common.Output{Height: height, TxId: i, Value: v.Value,
				Address: &receiver, N: uint32(j), Assets: v.Assets}
			outputs = append(outputs, output)
		}

		txs[i] = &common.Transaction{
			Txid:    tx.TxID(),
			Inputs:  inputs,
			Outputs: outputs,
		}
	}

	t := block.Header.Timestamp
	return &common.Block{
		Timestamp:     t,
		Height:        height,
		Hash:          block.BlockHash().String(),
		PrevBlockHash: block.Header.PrevBlock.String(),
		Transactions:  txs,
	}
}


// Prefetches blocks from bitcoind and sends them to the blocksChan
func (b *BaseIndexer) spawnBlockFetcher(startHeigh int, endHeight int, stopChan chan struct{}) {
	currentHeight := startHeigh
	for currentHeight <= endHeight {
		select {
		case <-stopChan:
			return
		default:
			block := b.fetchBlock(currentHeight)
			b.blocksChan <- block
			currentHeight += 1
		}
	}

	<-stopChan
}

func (b *BaseIndexer) drainBlocksChan() {
	for {
		select {
		case <-b.blocksChan:
		default:
			return
		}
	}
}
