package main

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/btcwallet"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

func showGenesisBlock(chainParams *chaincfg.Params) {

	// Show Block info
	showBlock(chainParams.GenesisBlock)

}

func parseAddress(address string, chainParams *chaincfg.Params) {

	pkScript, err := btcwallet.AddrToPkScript(address)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("    pkScript: %x", pkScript)
}

func parsePkScript(pkscript string, chainParams *chaincfg.Params) {

	pkBytes, _ := hex.DecodeString(pkscript)
	address, err := btcwallet.PkScriptToAddr(pkBytes)
	fmt.Printf("pkScript: %x", pkBytes)
	if err != nil {
		log.Errorf("PkScriptToAddr failed: %v ", err)
	} else {
		fmt.Printf("address: %s", address)
	}
}

func GenerateGenesisBlock(chainParams *chaincfg.Params) {

	genesisTime := time.Now()
	// Create a genesis TX
	genesisTx, _ := createGenesisTx(chainParams, genesisTime)

	btcwallet.LogMsgTx(genesisTx)

	nonce := rand.Uint32()

	genesisBlock := &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:    1,
			PrevBlock:  chainhash.Hash{},
			MerkleRoot: genesisTx.TxHash(),
			Timestamp:  genesisTime,
			Bits:       0,
			Nonce:      nonce,
		},
		Transactions: []*wire.MsgTx{genesisTx},
	}

	showBlock(genesisBlock)
	fmt.Printf("timestamp: %d %s\n", genesisTime.Unix(), genesisTime.String())
	logHash("var genesisMerkleRoot = chainhash.Hash", genesisBlock.Header.MerkleRoot[:])
	blockHash := genesisBlock.BlockHash()
	logHash("var satsNetGenesisHash = chainhash.Hash", blockHash[:])
	logHash("var genesisCoinbaseScript = []byte", genesisTx.TxIn[0].SignatureScript)
	logHash("var genesisTxOutPkScript = []byte", genesisTx.TxOut[0].PkScript)
}

func showBlock(block *wire.MsgBlock) {
	// Show Block info
	fmt.Printf("-------------------------  Block Header  --------------------------\n")
	fmt.Printf("    Block Hash: %s\n", block.BlockHash().String())

	fmt.Printf("    Block Version: %d\n", block.Header.Version)

	fmt.Printf("    Prev Block Hash: %s\n", block.Header.PrevBlock.String())

	fmt.Printf("    Block MerkleRoot Hash: %s\n", block.Header.MerkleRoot.String())

	fmt.Printf("    Block TimeStamp Unix: %d\n", block.Header.Timestamp.Unix())
	fmt.Printf("    Block TimeStamp: %s\n", block.Header.Timestamp.Format(time.DateTime))

	fmt.Printf("    Block Bits: %d\n", block.Header.Bits)

	fmt.Printf("    Block Nonce: %d\n", block.Header.Nonce)

	fmt.Printf("-------------------------  End  --------------------------\n")

	fmt.Printf("-------------------------  Block Transactions  --------------------------\n")
	transactions := block.Transactions
	for _, tx := range transactions {
		btcwallet.LogMsgTx(tx)
	}
	fmt.Printf("-------------------------  End  --------------------------\n")
}

// createCoinbaseTx returns a coinbase transaction paying an appropriate subsidy
// based on the passed block height to the provided address.  When the address
// is nil, the coinbase transaction will instead be redeemable by anyone.
//
// See the comment for NewBlockTemplate for more information about why the nil
// address handling is useful.
func createGenesisTx(chainParams *chaincfg.Params, timeStamp time.Time) (*wire.MsgTx, error) {

	
	coinbaseScript, err := StandardGenesisScript([]byte("Not your keys, not your coins. Don't trust. Verify."), timeStamp.Unix())
	if err != nil {
		panic(err)
	}

	pubkeyBytes, _ := hex.DecodeString(indexer.GetBootstrapPubKey())
	pubKey, err := secp256k1.ParsePubKey(pubkeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %v", err)
	}

	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	taprootAddr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), chainParams)
	if err != nil {
		return nil, err
	}
	pkScript, err := txscript.PayToAddrScript(taprootAddr)
	if err != nil {
		return nil, err
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{
		// Coinbase transactions have no inputs, so previous outpoint is
		// zero hash and max index.
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{},
			wire.MaxPrevOutIndex),
		SignatureScript: coinbaseScript,
		Sequence:        wire.MaxTxInSequenceNum,
	})
	tx.AddTxOut(&wire.TxOut{
		Value:    0, // For satoshinet, no award.
		Assets:   wire.TxAssets{},
		PkScript: pkScript,
	})
	return tx, nil
}

func calcMerkleRoot(txns []*wire.MsgTx) chainhash.Hash {
	if len(txns) == 0 {
		return chainhash.Hash{}
	}

	utilTxns := make([]*btcutil.Tx, 0, len(txns))
	for _, tx := range txns {
		utilTxns = append(utilTxns, btcutil.NewTx(tx))
	}
	return blockchain.CalcMerkleRoot(utilTxns, false)
}

func logHash(title string, data []byte) {
	fmt.Printf("%s: {\n        ", title)
	lineCount := 0
	for i := 0; i < len(data); i++ {
		fmt.Printf("0x%02x, ", data[i])
		if lineCount == 7 {
			fmt.Printf("\n        ")
			lineCount = 0
			continue
		}
		lineCount++
	}
	fmt.Printf("\n")
	fmt.Printf("}\n")
}
