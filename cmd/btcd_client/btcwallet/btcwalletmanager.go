package btcwallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	HOST_BTCCHAIN_RELEASE = "https://blockstream.info/api"
	HOST_BTCCHAIN_TEST    = "http://192.168.10.104:8019/testnet"

	URL_GETTXLIST  = "%s/address/%s/txs"
	URL_GETBALANCE = "%s/address/%s"
	URL_GETUTXOS   = "%s/allutxos/address/%s"
	URL_TRANSATION = "%s/tx"
)

type BTCWalletManager struct {
	BTCWalletList map[string]*BTCWallet
}

type BTCAccountStatus struct {
	Funded_TXO_Count int64 `json:"funded_txo_count"`
	Funded_TXO_Sum   int64 `json:"funded_txo_sum"`
	Spent_TXO_Count  int64 `json:"spent_txo_count"`
	Spent_TXO_Sum    int64 `json:"spent_txo_sum"`
	TX_Count         int64 `json:"tx_count"`
}

type BTCAddress struct {
	Address      string           `json:"address"`
	ChainStats   BTCAccountStatus `json:"chain_stats"`
	MempoolStats BTCAccountStatus `json:"mempool_stats"`
}

/*
	TX Sample
	{
		"txid":"b9e26347105c9c795d745683ee14fa33d5543d3fa4c96b986915ee5294d15e0b",
		"version":2,
		"locktime":2542025,
		"vin":
		[
			{
				"txid":"c7b5766a6342fcc1752be759db520f52060932f83178ef1695a9da890766193a",
				"vout":1,
				"prevout":
				{
					"scriptpubkey":"0014a57a0f6c6d1b1dfc74ffd2c7bef9422e12c4572e",
					"scriptpubkey_asm":"OP_0 OP_PUSHBYTES_20 a57a0f6c6d1b1dfc74ffd2c7bef9422e12c4572e",
					"scriptpubkey_type":"v0_p2wpkh",
					"scriptpubkey_address":"tb1q54aq7mrdrvwlca8l6trma72z9cfvg4ewcnmj95",
					"value":2241425262
				},
				"scriptsig":"",
				"scriptsig_asm":"",
				"witness":
				[
					"30440220553ea023e28df4d49234b502cb0f648779c685174bb22e87df2e3157e6b0b01602201eb469335eec7fcbb12a595125f199ea49d743288fdde34515bbd1883553977a01",
					"02c0f3894147214a1ef1fdc9e6d82b5fe1a3de39f450f65ed356c6f1b9f7838a48"
				],
				"is_coinbase":false,
				"sequence":4294967293
			}
		],
		"vout":
		[
			{
				"scriptpubkey":"76a914d1696072a0c1572a315361c54ff7092b10d1500e88ac",
				"scriptpubkey_asm":"OP_DUP OP_HASH160 OP_PUSHBYTES_20 d1696072a0c1572a315361c54ff7092b10d1500e OP_EQUALVERIFY OP_CHECKSIG",
				"scriptpubkey_type":"p2pkh",
				"scriptpubkey_address":"mzcDkrLaLv5w49N4ua9vsf8KQsMUbkZSxj",
				"value":1565492
			},
			{
				"scriptpubkey":"76a9143851f0e7dd33eacb12dfd5232281026113c943ac88ac",
				"scriptpubkey_asm":"OP_DUP OP_HASH160 OP_PUSHBYTES_20 3851f0e7dd33eacb12dfd5232281026113c943ac OP_EQUALVERIFY OP_CHECKSIG",
				"scriptpubkey_type":"p2pkh",
				"scriptpubkey_address":"mkekJADL1gZANf7hMRv6HG6GTWMDxMUkkv",
				"value":2239845070
			}
		],
		"size":228,
		"weight":585,
		"fee":14700,
		"status":
		{
			"confirmed":true,
			"block_height":2542026,
			"block_hash":"00000000000076069b005c11c0466c4f30ce480eb81cd4825cb1173266ca8228",
			"block_time":1702024488
		}
	}
*/

// vout
type BTCTXVOut struct {
	ScriptPubKey        string `json:"scriptpubkey"`
	SpriptPubkeyAsm     string `json:"scriptpubkey_asm"`
	SpriptPubkeyType    string `json:"scriptpubkey_type"`
	SpriptPubkeyAddress string `json:"scriptpubkey_address"`
	Value               int64  `json:"value"`
}

// vin
type BTCTXVIn struct {
	TX_ID        string    `json:"txid"`
	Vout         int64     `json:"vout"`
	Prevout      BTCTXVOut `json:"prevout"`
	ScriptSig    string    `json:"scriptsig"`
	ScriptSigAsm string    `json:"scriptsig_asm"`
	Witness      []string  `json:"witness"`
	IsCoinBase   bool      `json:"is_coinbase"`
	Sequence     int64     `json:"sequence"`
}

// status
type BTCTXStatus struct {
	Confirmed   bool   `json:"confirmed"`
	BlockHeight int64  `json:"block_height"`
	BlockHash   string `json:"block_hash"`
	BlockTime   int64  `json:"block_time"`
}

// BTC tx
type BTCTXStruct struct {
	TX_ID    string      `json:"txid"`
	Version  int         `json:"version"`
	LockTime int64       `json:"locktime"`
	VIn      []BTCTXVIn  `json:"vin"`
	VOut     []BTCTXVOut `json:"vout"`
	Size     int64       `json:"size"`
	Weight   int64       `json:"weight"`
	Fee      int64       `json:"fee"`
	Status   BTCTXStatus `json:"status"`
}

type BTCInOutSummary struct {
	Address string `json:"address"`
	Value   int64  `json:"value"`
}

type BTCTXSummaryStruct struct {
	TX_ID     string            `json:"txid"`
	TxTime    int64             `json:"txtime"`
	In        []BTCInOutSummary `json:"in"`
	Out       []BTCInOutSummary `json:"out"`
	Value     int64             `json:"size"`
	Fee       int64             `json:"fee"`
	Confirmed bool              `json:"confirmed"`
}

type BTCUtxo struct {
	TXID  string `json:"txid"`
	VOut  int64  `json:"vout"`
	Value int64  `json:"value"`
}

type BTCUtxoResponse struct {
	Code  int       `json:"code"`
	Msg   string    `json:"msg"`
	Total int       `json:"total"`
	Utxos []BTCUtxo `json:"otherutxos"`
}

type BTCTxRes struct {
	TXID string `json:"txid"`
}

/*
// UTXO respones from blockstream
{
	"txid":"79b15ac2052d3f993148d12694675b294d81b861367d0f86841170f442d17d18",
	"vout":1,
	"status":{
		"confirmed":true,
		"block_height":2543474,
		"block_hash":"00000000000000108499459b66dba3b43d477a3b8b49062b8603259f36c5cf6d",
		"block_time":1703056613
	},
	"value":1000
}
*/

var (
	BtcWalletMgr *BTCWalletManager
	NetParams    *chaincfg.Params
)

func ResetInstance() {
	BtcWalletMgr = nil
}
func GetWalletInst() *BTCWalletManager {
	if BtcWalletMgr == nil {
		return nil
	}
	return BtcWalletMgr
}

func InitWalletManager(workDir string, params *chaincfg.Params) {
	NetParams = params
	if BtcWalletMgr == nil {
		BtcWalletMgr = &BTCWalletManager{}
		BtcWalletMgr.BTCWalletList = make(map[string]*BTCWallet)
		BtcWalletMgr.InitBTCWalletList(workDir)
	}
	return
}

func (walletMgr *BTCWalletManager) InitBTCWalletList(workDir string) error {
	fmt.Println("InitBTCWalletList start... ")
	if walletMgr.BTCWalletList == nil {
		err := fmt.Errorf("BTCWalletManager isnot initialed")
		fmt.Println(err.Error())
		return err
	}
	LoadLocalWallet(workDir)
	//userAccount := useraccountmanager.GetInstance()
	accounts, err := GetAllWalletAccounts()
	if err != nil {
		// InitBTCWallet
		fmt.Printf("InitBTCWallet failed: %v\n", err)
		return err
	}
	for _, accountName := range accounts {
		fmt.Printf("Loading btc wallet: %s ... \n", accountName)
		wallet, err := InitBTCWallet(accountName, workDir)
		if err != nil {
			// InitBTCWallet
			fmt.Printf("InitBTCWallet failed: %v\n", err)
			continue
		}
		walletMgr.BTCWalletList[accountName] = wallet
	}

	fmt.Println("InitBTCWalletList done. ")
	return nil
}

func (walletMgr *BTCWalletManager) GetWallet(walletName string) (*BTCWallet, error) {
	fmt.Println("GetWallet start... ")
	if walletMgr.BTCWalletList == nil {
		err := fmt.Errorf("BTCWalletManager isnot initialed")
		fmt.Printf("GetWallet failed: %s\n", err)
		return nil, err
	}
	wallet := walletMgr.BTCWalletList[walletName]
	if wallet == nil {
		err := fmt.Errorf("cannot found wallet: %s", walletName)
		fmt.Printf("GetWallet failed: %s\n", err)
		return nil, err
	}

	fmt.Println("GetWallet done.")
	return wallet, nil
}

func (walletMgr *BTCWalletManager) AddBTCWallet(wallet *BTCWallet) error {
	if wallet == nil {
		err := fmt.Errorf("AddBTCWallet: invalid btc wallet")
		fmt.Println(err.Error())
		return err
	}

	fmt.Printf("AddBTCWallet: %s\n", wallet.walletName)
	if walletMgr.BTCWalletList == nil {
		err := fmt.Errorf("BTCWalletManager isnot initialed")
		fmt.Println(err.Error())
		return err
	}

	walletExist := walletMgr.BTCWalletList[wallet.walletName]
	if walletExist != nil {
		err := fmt.Errorf("the wallet %s has existed in manager", wallet.walletName)
		fmt.Printf("AddBTCWallet failed: %s\n", err.Error())
		return err
	}

	// The child wallet has not create, create it
	walletMgr.BTCWalletList[wallet.walletName] = wallet

	fmt.Println("AddBTCWallet done. ")
	return nil
}
func (walletMgr *BTCWalletManager) DeleteWallet(walletname string) error {
	fmt.Printf("DeleteWallet: %s\n", walletname)
	if walletMgr.BTCWalletList == nil {
		err := fmt.Errorf("BTCWalletManager isnot initialed")
		fmt.Println(err.Error())
		return err
	}

	wallet := walletMgr.BTCWalletList[walletname]
	if wallet == nil {
		err := fmt.Errorf("DeleteWallet: invalid btc wallet")
		fmt.Println(err.Error())
		return err
	}

	// Delete from wallet list
	delete(walletMgr.BTCWalletList, walletname)

	// delete from account manager
	//userAccount := useraccountmanager.GetInstance()
	err := DelChildAccount(walletname)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	fmt.Println("DeleteWallet done. ")
	return nil
}

func (walletMgr *BTCWalletManager) GetAllBTCWallets() map[string]*BTCWallet {
	fmt.Println("GetAllBTCWallets ...")
	for walletname, _ := range walletMgr.BTCWalletList {
		fmt.Println(walletname)
	}
	return walletMgr.BTCWalletList
}

func (walletMgr *BTCWalletManager) GetDefaultWallet() (*BTCWallet, error) {
	fmt.Println("GetAllBTCWallets ...")
	walletName := GetDefaultWalletName()
	if walletName == "" {
		err := fmt.Errorf("cannot found default wallet")
		fmt.Println(err.Error())
		return nil, err
	}
	wallet, err := walletMgr.GetWallet(walletName)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	return wallet, nil
}

func (walletMgr *BTCWalletManager) GetDefaultAddress() (string, error) {
	fmt.Println("GetDefaultAddress ...")
	wallet, err := walletMgr.GetDefaultWallet()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	address, err := wallet.GetDefaultAddress()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	return address, nil
}

func GetBTCBalance1(address string) (*big.Float, *big.Float, error) {
	fmt.Printf("GetBTCBalance %s ...\n", address)

	//fmt.Println("GetBTCBalance response : %+v", resAddress)
	//var amount btcutil.Amount
	amountTotal := btcutil.Amount(1000000)
	amountSpent := btcutil.Amount(0)

	amountTotal_mempool := btcutil.Amount(0)
	amountSpent_mempool := btcutil.Amount(0)

	amount := amountTotal - amountSpent
	amount_mempool := amountTotal_mempool - amountSpent_mempool

	result := big.NewFloat(amount.ToUnit(btcutil.AmountBTC))
	result_mempool := big.NewFloat(amount_mempool.ToUnit(btcutil.AmountBTC))

	fmt.Printf("Address %s Balance: chain =%s, mempool =%s\n", address, result.String(), result_mempool.String())
	//result := big.NewFloat(resAddress.ChainStats.Funded_TXO_Sum)
	return result, result_mempool, nil
}
func GetBTCBalance(address string) (*big.Float, *big.Float, error) {
	fmt.Printf("GetBTCBalance %s ...\n", address)
	var host string
	//if defines.RUNTIME_ENVIRONMENT == defines.ENVIRONMENT_TEST {
	host = HOST_BTCCHAIN_TEST
	//} else {
	//host = HOST_BTCCHAIN_RELEASE
	//}

	url := fmt.Sprintf(URL_GETBALANCE, host, address)
	//fmt.Println("GetBTCBalance request url is:%s", url)
	respJson, err := http.Get(url)
	if err != nil {
		fmt.Printf("GetBTCBalance: Get Balance request failed: %v\n", err)
		return nil, nil, err
	}
	data, err := io.ReadAll(respJson.Body)
	if err != nil {
		fmt.Printf("GetBTCBalance: Read response failed: %v\n", err)
		return nil, nil, err
	}

	fmt.Printf("GetBTCBalance response data: %s\n", string(data))
	resAddress := &BTCAddress{}
	json.Unmarshal(data, &resAddress)

	//fmt.Println("GetBTCBalance response : %+v", resAddress)
	//var amount btcutil.Amount
	amountTotal := btcutil.Amount(resAddress.ChainStats.Funded_TXO_Sum)
	amountSpent := btcutil.Amount(resAddress.ChainStats.Spent_TXO_Sum)

	amountTotal_mempool := btcutil.Amount(resAddress.MempoolStats.Funded_TXO_Sum)
	amountSpent_mempool := btcutil.Amount(resAddress.MempoolStats.Spent_TXO_Sum)

	amount := amountTotal - amountSpent
	amount_mempool := amountTotal_mempool - amountSpent_mempool

	result := big.NewFloat(amount.ToUnit(btcutil.AmountBTC))
	result_mempool := big.NewFloat(amount_mempool.ToUnit(btcutil.AmountBTC))

	fmt.Printf("Address %s Balance: chain =%s, mempool =%s\n", address, result.String(), result_mempool.String())
	//result := big.NewFloat(resAddress.ChainStats.Funded_TXO_Sum)
	return result, result_mempool, nil
}

func GetBTCUtoxs(address string) ([]*BTCUtxo, error) {
	//utxomap, err := getBTCUtoxs(address)
	utxomap, err := getBTCUtoxsWithBlock(address)
	if err != nil {
		return nil, err
	}
	utxoList := make([]*BTCUtxo, 0)

	for op, v := range utxomap {
		txid := op.Hash.String()
		vout := op.Index
		utxoInfo := &BTCUtxo{
			TXID:  txid,
			VOut:  int64(vout),
			Value: int64(v.value),
		}

		utxoList = append(utxoList, utxoInfo)
	}
	return utxoList, nil
}

func getBTCUtoxs(address string) (map[wire.OutPoint]*utxo, error) {
	fmt.Printf("GetBTCUtoxs %s ...\n", address)
	var host string
	//if defines.RUNTIME_ENVIRONMENT == defines.ENVIRONMENT_TEST {
	host = HOST_BTCCHAIN_TEST
	//} else {
	//host = HOST_BTCCHAIN_RELEASE
	//}

	url := fmt.Sprintf(URL_GETUTXOS, host, address)
	//fmt.Println("GetBTCUtoxs request url is:%s", url)
	respJson, err := http.Get(url)
	if err != nil {
		fmt.Printf("GetBTCUtoxs: Get Balance request failed: %v\n", err)
		return nil, err
	}
	data, err := io.ReadAll(respJson.Body)
	if err != nil {
		fmt.Printf("GetBTCUtoxs: Read response failed: %v\n", err)
		return nil, err
	}

	//fmt.Println("GetBTCUtoxs response data: %s", string(data))
	//resUtxos := make([]*BTCUtxo, 0)
	resUtxos := &BTCUtxoResponse{}
	//resUtxos := &BTCUtxoRes{}
	json.Unmarshal(data, &resUtxos)

	a, err := btcutil.DecodeAddress(address, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	selfAddrScript, err := txscript.PayToAddrScript(a)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	//fmt.Println("GetBTCUtoxs response data: %s", string(data))

	//pkscript := hex.EncodeToString(selfAddrScript)
	//fmt.Println("My address pkscript =  [% 2x]:  ", selfAddrScript)
	//fmt.Println("My address pkscript str =  %s:  ", hex.EncodeToString(selfAddrScript))
	logpkScript(selfAddrScript)

	utxos := make(map[wire.OutPoint]*utxo)
	for _, utxores := range resUtxos.Utxos {
		//fmt.Println("GetBTCUtoxs List index %d content: %+v", index, utxores)

		op := &wire.OutPoint{}
		Hash, err := chainhash.NewHashFromStr(utxores.TXID)
		if err != nil {
			fmt.Printf("GetBTCUtoxs: Read response failed: %v\n", err)
			return nil, err
		}
		op.Hash = *Hash
		op.Index = uint32(utxores.VOut)
		utxo := &utxo{
			value:    btcutil.Amount(utxores.Value),
			keyIndex: uint32(utxores.VOut),
			pkScript: selfAddrScript,
		}
		utxos[*op] = utxo

		fmt.Println("GetBTCUtoxs txid: ", utxores.TXID)
		fmt.Println("GetBTCUtoxs op: ", op)
		fmt.Println("GetBTCUtoxs utxo: ", utxo)
	}

	//var amount btcutil.Amount
	//amount := btcutil.Amount(resAddress.ChainStats.Funded_TXO_Sum)

	//result := big.NewFloat(amount.ToUnit(btcutil.AmountBTC))
	return utxos, nil
}

func getBTCUtoxs1(address string) (map[wire.OutPoint]*utxo, error) {
	fmt.Printf("GetBTCUtoxs %s ...\n", address)
	utxos := make(map[wire.OutPoint]*utxo)
	a, err := btcutil.DecodeAddress(address, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	selfAddrScript, err := txscript.PayToAddrScript(a)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	op := &wire.OutPoint{}
	Hash, err := chainhash.NewHashFromStr("3ccd36c9213e7b41babb6b05c3ccaa3a6977d39210adf042b8bdaddb1777dd37")
	if err != nil {
		fmt.Printf("GetBTCUtoxs: Read response failed: %v\n", err)
		return nil, err
	}
	op.Hash = *Hash
	op.Index = 0
	utxo := &utxo{
		value:    1000000,
		keyIndex: 0,
		pkScript: selfAddrScript,
		txAssets: wire.TxAssets{},
	}
	utxos[*op] = utxo

	//var amount btcutil.Amount
	//amount := btcutil.Amount(resAddress.ChainStats.Funded_TXO_Sum)

	//result := big.NewFloat(amount.ToUnit(btcutil.AmountBTC))
	return utxos, nil
}

func getBTCUtoxsWithBlock(address string) (map[wire.OutPoint]*utxo, error) {
	fmt.Printf("getBTCUtoxsWithBlock %s ...\n", address)
	utxos := make(map[wire.OutPoint]*utxo)
	a, err := btcutil.DecodeAddress(address, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	selfAddrScript, err := txscript.PayToAddrScript(a)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	/*
		op := &wire.OutPoint{}
		Hash, err := chainhash.NewHashFromStr("3ccd36c9213e7b41babb6b05c3ccaa3a6977d39210adf042b8bdaddb1777dd37")
		if err != nil {
			fmt.Printf("GetBTCUtoxs: Read response failed: %v\n", err)
			return nil, err
		}
		op.Hash = *Hash
		op.Index = 0
		utxo := &utxo{
			value:      1000000,
			keyIndex:   0,
			pkScript:   selfAddrScript,
			satsRanges: []wire.SatsRange{{Start: 2000000, Size: 500000}, {Start: 5000000, Size: 500000}},
		}
	*/
	heightBlock, err := getBlockBest()
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	for height := heightBlock; height > 0; height-- {
		utxosBlock, err := getUtxosFromBlock(selfAddrScript, height)
		if err != nil {
			fmt.Println(err.Error())
			return nil, err
		}
		//utxos[*op] = utxo

		for op, utxo := range utxosBlock {
			// if utxo.value > 10000 {
			// 	continue
			// }
			utxos[op] = utxo
		}

		if len(utxos) >= 2 {
			// Just leave last utxo to avoid the utxo is spent
			break
		}
	}

	return utxos, nil
}

func getUtxosFromBlock(pkScxript []byte, height uint32) (map[wire.OutPoint]*utxo, error) {
	//fmt.Println("Block: ", height)
	hash, err := satsnet_rpc.GetBlockHash(int64(height))
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	//fmt.Println("Block Hash: ", hash.String())
	block, err := satsnet_rpc.GetRawBlock(hash)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	// blockData, err := hex.DecodeString(rawBlock)
	// if err != nil {
	// 	log.Errorf("syncBlock-> Failed to decode block: %v", err)
	// 	return err
	// }

	// Deserialize the bytes into a btcutil.Block.
	// block, err := btcutil.NewBlockFromBytes(blockData)
	// if err != nil {
	// 	//log.Panicf("syncBlock-> Failed to parse block: %v", err)
	// 	log.Error(err)
	// 	return err
	// }

	utxos := make(map[wire.OutPoint]*utxo)
	transactions := block.Transactions
	for _, tx := range transactions {
		for index, out := range tx.TxOut {
			if bytes.Equal(pkScxript, out.PkScript) {
				// The output is for the wallet, so create a txin from it
				op := &wire.OutPoint{}
				op.Hash = tx.TxHash()
				op.Index = uint32(index)
				utxo := &utxo{
					value:          btcutil.Amount(out.Value),
					keyIndex:       uint32(index),
					pkScript:       out.PkScript,
					txAssets:       out.Assets,
					maturityHeight: int32(height),
				}
				utxos[*op] = utxo
			}
		}
	}

	//fmt.Println("Parse Block done.")
	return utxos, nil
}
func getBlockBest() (uint32, error) {
	fmt.Println("getBlockBest: ")
	height, err := satsnet_rpc.GetBlockCount()
	if err != nil {
		fmt.Println(err.Error())
		return 0, err
	}

	fmt.Println("Block eight: ", height)
	return uint32(height), nil
}

func SendTransaction(raw string) (string, error) {
	fmt.Printf("Transaction %s ...\n", raw)
	// var host string
	// if defines.RUNTIME_ENVIRONMENT == defines.ENVIRONMENT_TEST {
	// 	host = HOST_BTCCHAIN_TEST
	// } else {
	// 	host = HOST_BTCCHAIN_RELEASE
	// }

	// url := fmt.Sprintf(URL_TRANSATION, host)
	// fmt.Println("Transaction request url is:%s", url)
	// respJson, err := http.Post(url, "application/json", strings.NewReader(raw))
	// if err != nil {
	// 	fmt.Println("Transaction:Post request failed: %v", err)
	// 	return "", err
	// }
	// data, err := io.ReadAll(respJson.Body)
	// if err != nil {
	// 	fmt.Println("Transaction: Read response failed: %v", err)
	// 	return "", err
	// }

	// if isTxid(string(data)) == true {
	// 	fmt.Println("Transaction response data: %s", string(data))
	// 	txid := string(data)

	// 	return txid, nil
	// }
	// err = errors.New(string(data))
	// fmt.Println(err.Error)
	return "", nil
}

func isTxid(txRes string) bool {
	if len(txRes) == 64 && strings.Contains(txRes, "error") == false {
		return true
	}
	return false
}

func GetRawTransaction(tx *wire.MsgTx, allowHighFees bool) (string, error) {
	fmt.Println("GetRawTransaction ...")
	txHex := ""
	if tx != nil {
		// Serialize the transaction and convert to hex string.
		buf := bytes.NewBuffer(make([]byte, 0, tx.SerializeSize()))
		if err := tx.Serialize(buf); err != nil {
			return "", err
		}
		fmt.Println("txHex raw: [% 2x]", buf)
		txHex = hex.EncodeToString(buf.Bytes())
	}

	cmd := btcjson.NewSendRawTransactionCmd(txHex, &allowHighFees)
	fmt.Printf("txHex: %s\n", txHex)
	fmt.Printf("cmd: %s\n", cmd)

	return txHex, nil
}

// Get all tx list
func GetAllTxList(address string, startPage int, offset int) ([]*BTCTXStruct, error) {
	//fmt.Println("GetAllTxList %s with page %d, offset %d...", address, startPage, offset)
	fmt.Printf("GetBTCTXList %s ...\n", address)
	var host string
	//if defines.RUNTIME_ENVIRONMENT == defines.ENVIRONMENT_TEST {
	//	host = HOST_BTCCHAIN_TEST
	//} else {
	host = HOST_BTCCHAIN_RELEASE
	//}

	url := fmt.Sprintf(URL_GETTXLIST, host, address)
	fmt.Printf("GetBTCTXList request url is:%s\n", url)
	respJson, err := http.Get(url)
	if err != nil {
		fmt.Printf("GetBTCTXList: Request failed: %v\n", err)
		return nil, err
	}
	data, err := io.ReadAll(respJson.Body)
	if err != nil {
		fmt.Printf("GetBTCTXList: Read response failed: %v\n", err)
		return nil, err
	}

	//fmt.Println("GetBTCTXList response data: %s", string(data))

	txList := make([]*BTCTXStruct, 0)
	if len(data) > 0 {
		err = json.Unmarshal(data, &txList)
		if err != nil {
			fmt.Printf("GetBTCTXList: Unmarshal failed: %v\n", err)
			return nil, err
		}
	}

	//for index, tx := range txList {
	//	fmt.Println("GetBTCTXList List index %d content: %+v", index, tx)
	//}

	return txList, nil
}

// Get all tx list
func GetAllTxSummary(address string) ([]*BTCTXSummaryStruct, error) {
	//fmt.Println("GetAllTxList %s with page %d, offset %d...", address, startPage, offset)
	fmt.Printf("GetAllTxSummary %s ...\n", address)

	txlist, err := GetAllTxList(address, 0, 0)
	if err != nil {
		fmt.Printf("BTC Wallet: get btc tx list failed %v.\n", err)
		return nil, err
	}

	txSummaryList := make([]*BTCTXSummaryStruct, 0)

	for _, tx := range txlist {
		txSummary := &BTCTXSummaryStruct{}
		value := int64(0)
		vin := make(map[string]int64)
		vout := make(map[string]int64)

		for _, vIn := range tx.VIn {
			if vIn.Prevout.SpriptPubkeyAddress == address {
				value -= vIn.Prevout.Value
			}

			vinValue := vin[vIn.Prevout.SpriptPubkeyAddress]
			vinValue += vIn.Prevout.Value
			vin[vIn.Prevout.SpriptPubkeyAddress] = vinValue
		}
		for _, vOut := range tx.VOut {
			if vOut.SpriptPubkeyAddress == address {
				value += vOut.Value
			}

			voutValue := vin[vOut.SpriptPubkeyAddress]
			voutValue += vOut.Value
			vout[vOut.SpriptPubkeyAddress] = voutValue
		}

		txSummary.In = make([]BTCInOutSummary, 0)
		for inAddress, value := range vin {
			BTCInOutSummary := BTCInOutSummary{
				Address: inAddress,
				Value:   value,
			}
			txSummary.In = append(txSummary.In, BTCInOutSummary)
		}

		txSummary.Out = make([]BTCInOutSummary, 0)
		for OutAddress, value := range vout {
			BTCInOutSummary := BTCInOutSummary{
				Address: OutAddress,
				Value:   value,
			}
			txSummary.Out = append(txSummary.Out, BTCInOutSummary)
		}

		txSummary.TX_ID = tx.TX_ID
		txSummary.Confirmed = tx.Status.Confirmed
		txSummary.TxTime = tx.Status.BlockTime
		txSummary.Value = value
		txSummary.Fee = tx.Fee

		txSummaryList = append(txSummaryList, txSummary)
	}

	return txSummaryList, nil
}

func logpkScript(pkScript []byte) {

	switch {
	case txscript.IsPayToScriptHash(pkScript):
		fmt.Println("The pkScript IsPayToScriptHash: P2SH")

	case txscript.IsPayToWitnessScriptHash(pkScript):
		fmt.Println("The pkScript IsPayToWitnessScriptHash:P2WSH")

	case txscript.IsPayToWitnessPubKeyHash(pkScript):
		fmt.Println("The pkScript IsPayToWitnessPubKeyHash: P2WKH")

	case txscript.IsPayToTaproot(pkScript):
		fmt.Println("The pkScript IsTaprootKeyHash: Taproot")

	case txscript.IsPayToPubKeyHash(pkScript):
		fmt.Println("The pkScript is IsPayToPublicKeyHash: P2PKH")

	default:
		fmt.Println("The pkScript is unknow")

	}

}

func LogAddressType(address string) {
	a, err := btcutil.DecodeAddress(address, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	addrScript, err := txscript.PayToAddrScript(a)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	//pkscript := hex.EncodeToString(selfAddrScript)
	fmt.Printf("My address pkscript =  [%x]:  \n", addrScript)
	logpkScript(addrScript)
}

func AddrToPkScript(addr string) ([]byte, error) {
	address, err := btcutil.DecodeAddress(addr, NetParams)
	if err != nil {
		return nil, err
	}

	return txscript.PayToAddrScript(address)
}

func PkScriptToAddr(pkScript []byte) (string, error) {

	if len(pkScript) > 0 && pkScript[0] == txscript.OP_RETURN {
		err := fmt.Errorf("pkscript is OP_RETURN Script")
		return "", err
	}

	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, NetParams)
	if err != nil {
		return "", err
	}

	if len(addrs) == 0 {
		err := fmt.Errorf("failed to get addr with pkscript [%x]", pkScript)
		return "", err
	}

	return addrs[0].EncodeAddress(), nil

}
