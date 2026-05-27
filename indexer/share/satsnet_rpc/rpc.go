package satsnet_rpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

func GetTx(txid string) (*btcutil.Tx, error) {
	hash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		return nil, err
	}

	ret, err := _client.client.GetRawTransaction(hash)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func GetTxVerbose(txid string) (*btcjson.TxRawResult, error) {
	hash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		return nil, err
	}

	ret, err := _client.client.GetRawTransactionVerbose(hash)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func IsUtxoSpent(utxo string) (bool, error) {
	parts := strings.Split(utxo, ":")
	hash, err := chainhash.NewHashFromStr(parts[0])
	if err != nil {
		return true, fmt.Errorf("invalid transaction ID: %v", err)
	}
	vout, _ := strconv.Atoi(parts[1])
	txOut, err := _client.client.GetTxOut(hash, uint32(vout), true)
	if err != nil {
		return true, fmt.Errorf("error checking UTXO: %v", err)
	}
	// 如果 txOut 为 nil，说明 UTXO 已被花费
	return txOut == nil, nil
}

func isUtxoSpentInMempool(txid string, vout int) (bool, error) {
	// 将交易ID字符串转换为chainhash.Hash
	hash, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		return false, fmt.Errorf("invalid transaction ID: %v", err)
	}

	// 使用 GetMempoolEntry 检查交易是否在 mempool 中
	_, err = _client.client.GetMempoolEntry(txid)
	if err == nil {
		// 如果交易在 mempool 中，utxo也有可能已经被花费，只是都没有确认
		return false, nil
	}

	// 使用 GetRawTransaction 获取交易详情
	tx, err := _client.client.GetRawTransactionVerbose(hash)
	if err != nil {
		// 如果交易不存在，可能已经被花费
		// 使用 GetTxOut 进一步确认
		txOut, err := _client.client.GetTxOut(hash, uint32(vout), true)
		if err != nil {
			return false, fmt.Errorf("error checking UTXO: %v", err)
		}
		// 如果 txOut 为 nil，说明 UTXO 已被花费
		return txOut == nil, nil
	}

	// 如果交易存在且已确认，这个 UTXO 没有在 mempool 中被花费
	if tx.Confirmations > 0 {
		return false, nil
	}

	// 如果交易存在于 mempool 中，检查指定的 vout 是否仍然可用
	// for _, out := range tx.MsgTx().TxOut {
	//     if out.Value > 0 {
	//         return false, nil
	//     }
	// }

	// 如果所有输出都被花费，这个 UTXO 可能在 mempool 中被花费了
	return true, nil
}

// TODO 需要本地维护一个mempool，加快查询速度
func IsExistUtxoInMemPool(utxo string) (bool, error) {
	txid, vout, err := indexer.ParseUtxo(utxo)
	if err != nil {
		return false, err
	}
	return isUtxoSpentInMempool(txid, vout)
}

func EstimateSmartFee(confTarget int64, mode btcjson.EstimateSmartFeeMode) (*btcjson.EstimateSmartFeeResult, error) {
	return _client.client.EstimateSmartFee(confTarget, &mode)
}

func Call(method string, params []interface{}) (json.RawMessage, error) {
	if _client.client == nil {
		return nil, fmt.Errorf("satoshinet rpc client is not initialized")
	}
	rawParams := make([]json.RawMessage, len(params))
	for i, param := range params {
		b, err := json.Marshal(param)
		if err != nil {
			return nil, err
		}
		rawParams[i] = b
	}
	return _client.client.RawRequest(method, rawParams)
}

func TestRawTransaction(signedTxHex []string) ([]*btcjson.TestMempoolAcceptResult, error) {
	txs := make([]*wire.MsgTx, 0, len(signedTxHex))
	for _, tx := range signedTxHex {
		txBytes, err := hex.DecodeString(tx)
		if err != nil {
			return nil, err
		}

		msgTx := wire.NewMsgTx(wire.TxVersion)
		err = msgTx.Deserialize(bytes.NewReader(txBytes))
		if err != nil {
			return nil, err
		}
		txs = append(txs, msgTx)
	}

	resp, err := _client.client.TestMempoolAccept(txs, 0.1)
	if err != nil {
		return nil, err
	}

	for i, r := range resp {
		if !r.Allowed {
			accept := false
			if indexer.IsValidTx(r.RejectReason) {
				// 修改结果
				accept = true
			} else {
				// 看看是否可以获取到该tx
				_, err := GetTx(r.Txid)
				if err == nil {
					accept = true
				}
			}
			if accept {
				// 修改结果
				resp[i].Allowed = true
				resp[i].RejectReason = ""
			}
		}
	}

	return resp, nil
}

func SendRawTransaction(txHex string, allowHighFees bool) (*chainhash.Hash, error) {
	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		return nil, err
	}

	// 创建一个新的 MsgTx
	msgTx := wire.NewMsgTx(wire.TxVersion)

	// 从字节切片中解码 MsgTx
	err = msgTx.Deserialize(bytes.NewReader(txBytes))
	if err != nil {
		return nil, err
	}

	txId, err := _client.client.SendRawTransaction(msgTx, allowHighFees)
	if err != nil {
		errStr := err.Error()
		hash := msgTx.TxHash()
		if indexer.IsValidTx(errStr) {
			return &hash, nil
		} else {
			_, err2 := GetTx(msgTx.TxID())
			if err2 == nil {
				return &hash, nil
			} else {
				return nil, err
			}
		}
	}
	return txId, err
}

func GetBestBlock() (*chainhash.Hash, int32, error) {
	return _client.client.GetBestBlock()
}

func GetBlockCount() (int64, error) {
	return _client.client.GetBlockCount()
}

func GetBlockHash(height int64) (*chainhash.Hash, error) {
	return _client.client.GetBlockHash(height)
}

func GetRawBlock(blockHash *chainhash.Hash) (*wire.MsgBlock, error) {
	return _client.client.GetBlock(blockHash)
}

func GetRawBlock2(blockstr string) (*wire.MsgBlock, error) {
	hash, err := chainhash.NewHashFromStr(blockstr)
	if err != nil {
		return nil, err
	}
	return _client.client.GetBlock(hash)
}

func GetRawBlockVerbose(blockstr string) (*btcjson.GetBlockVerboseResult, error) {
	hash, err := chainhash.NewHashFromStr(blockstr)
	if err != nil {
		return nil, err
	}
	return _client.client.GetBlockVerbose(hash)
}

// EncodeMsgBlockToString takes a wire.MsgBlock and encodes it to a string
func EncodeMsgBlockToString(msgBlock *wire.MsgBlock) (string, error) {
	var buf bytes.Buffer
	err := msgBlock.Serialize(&buf) // Serialize the block into a byte buffer
	if err != nil {
		return "", err
	}

	// Convert the serialized byte buffer to a hexadecimal string
	return hex.EncodeToString(buf.Bytes()), nil
}

// DecodeStringToMsgBlock takes a string and decodes it to a wire.MsgBlock
func DecodeStringToMsgBlock(encodedStr string) (*wire.MsgBlock, error) {
	// Convert the hex string back to bytes
	blockBytes, err := hex.DecodeString(encodedStr)
	if err != nil {
		return nil, err
	}

	// Create a buffer from the byte slice
	buf := bytes.NewBuffer(blockBytes)

	// Create an empty MsgBlock to deserialize into
	msgBlock := wire.MsgBlock{}

	// Deserialize the bytes into the MsgBlock
	err = msgBlock.Deserialize(buf)
	if err != nil {
		return nil, err
	}

	return &msgBlock, nil
}

// EncodeTxToString takes a btcutil.Tx and encodes it to a string
func EncodeTxToString(tx *btcutil.Tx) (string, error) {
	var buf bytes.Buffer
	err := tx.MsgTx().Serialize(&buf) // Serialize the MsgTx into a byte buffer
	if err != nil {
		return "", err
	}

	// Convert the serialized byte buffer to a hexadecimal string
	return hex.EncodeToString(buf.Bytes()), nil
}

// DecodeStringToTx takes a string and decodes it to a btcutil.Tx
func DecodeStringToTx(encodedStr string) (*btcutil.Tx, error) {
	// Convert the hex string back to bytes
	txBytes, err := hex.DecodeString(encodedStr)
	if err != nil {
		return nil, err
	}

	// Create a buffer from the byte slice
	buf := bytes.NewBuffer(txBytes)

	// Create an empty MsgTx to deserialize into
	msgTx := wire.MsgTx{}

	// Deserialize the bytes into the MsgTx
	err = msgTx.Deserialize(buf)
	if err != nil {
		return nil, err
	}

	// Wrap the deserialized MsgTx into a btcutil.Tx and return
	return btcutil.NewTx(&msgTx), nil
}

// 提供一些接口，可以快速同步mempool中的数据，并将数据保存在本地kv数据库
// 1. 启动一个线程，或者一个被动的监听接口，监控内存池的新增tx的信息，
//    需要先获取mempool中所有tx（仅在初始化时调用），并且按照utxo为索引保存在数据库，
//    输入的utxo的spent为true，输出的utxo的spent为false
//    一个utxo很可能在生成后就马上被花费，所以生成时spent为false，被花费时设置为true
//    在上面的基础上，快速获取增量的tx（一般5s调用一次，期望10ms内完成操作）
// 2. 查询接口，查询一个utxo是否已经被花费，数据库查询，代替 IsExistUtxoInMemPool
// 3. 删除接口，删除一个UTXO（该utxo作为输入的tx所在block已经完成）
// 4. 以后可能会有很多基于内存池的操作，比如检查下内存池都是什么类型的tx，是否可以做RBF等等
