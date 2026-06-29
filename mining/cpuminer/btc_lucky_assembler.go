package cpuminer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcbtcjson "github.com/btcsuite/btcd/btcjson"
	btcbtcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btctxscript "github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
)

const coinbaseTag = "satoshinet btc lucky"

type assembledWork struct {
	block     *btcwire.MsgBlock
	coinbase  *btcwire.MsgTx
	header    btcwire.BlockHeader
	blockHash btcchainhash.Hash
}

func makeWorkerRanges(workers int) []WorkerRange {
	if workers < 1 {
		workers = 1
	}
	ranges := make([]WorkerRange, workers)
	baseExtraNonce := uint64(time.Now().UnixNano())
	for i := 0; i < workers; i++ {
		extraNonce := baseExtraNonce + uint64(i)
		ranges[i] = WorkerRange{
			WorkerID:        i,
			ExtraNonceStart: extraNonce,
			ExtraNonceEnd:   extraNonce,
		}
	}
	return ranges
}

func hashCompactJobHeader(job *CompactMiningJob, r WorkerRange, nonce uint32) (btcchainhash.Hash, error) {
	var zero btcchainhash.Hash
	if job == nil {
		return zero, fmt.Errorf("nil compact mining job")
	}
	if r.MerkleRoot == "" {
		return zero, fmt.Errorf("compact mining job missing worker merkle root")
	}
	prevHash, err := btcchainhash.NewHashFromStr(job.PreviousBlockHash)
	if err != nil {
		return zero, fmt.Errorf("invalid previous block hash: %w", err)
	}
	merkleRoot, err := btcchainhash.NewHashFromStr(r.MerkleRoot)
	if err != nil {
		return zero, fmt.Errorf("invalid merkle root: %w", err)
	}
	bits64, err := strconv.ParseUint(job.Bits, 16, 32)
	if err != nil {
		return zero, fmt.Errorf("invalid template bits %q: %w", job.Bits, err)
	}
	header := btcwire.BlockHeader{
		Version:    job.Version,
		PrevBlock:  *prevHash,
		MerkleRoot: *merkleRoot,
		Timestamp:  time.Unix(job.CurTime, 0),
		Bits:       uint32(bits64),
		Nonce:      nonce,
	}
	return header.BlockHash(), nil
}

func btcPayToAddrScript(addr string, params *btcchaincfg.Params) ([]byte, error) {
	decoded, err := btcbtcutil.DecodeAddress(addr, params)
	if err != nil {
		return nil, err
	}
	if !decoded.IsForNet(params) {
		return nil, fmt.Errorf("address %s is not for btc network %s", addr, params.Name)
	}
	return btctxscript.PayToAddrScript(decoded)
}

func assembleBTCWork(template *btcbtcjson.GetBlockTemplateResult, params *btcchaincfg.Params, rewardAddress string, extraNonce uint64, nonce uint32, ntime int64) (*assembledWork, error) {
	if template == nil {
		return nil, fmt.Errorf("nil block template")
	}
	if template.CoinbaseValue == nil {
		return nil, fmt.Errorf("getblocktemplate missing coinbasevalue")
	}

	prevHash, err := btcchainhash.NewHashFromStr(template.PreviousHash)
	if err != nil {
		return nil, fmt.Errorf("invalid previous block hash: %w", err)
	}
	bits64, err := strconv.ParseUint(template.Bits, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid template bits %q: %w", template.Bits, err)
	}

	coinbase, err := buildCoinbaseTx(template, params, rewardAddress, extraNonce)
	if err != nil {
		return nil, err
	}

	txs := make([]*btcwire.MsgTx, 0, 1+len(template.Transactions))
	txs = append(txs, coinbase)
	for _, txTemplate := range template.Transactions {
		raw, err := hex.DecodeString(txTemplate.Data)
		if err != nil {
			return nil, fmt.Errorf("decode template tx %s: %w", txTemplate.TxID, err)
		}
		tx := btcwire.NewMsgTx(btcwire.TxVersion)
		if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("deserialize template tx %s: %w", txTemplate.TxID, err)
		}
		txs = append(txs, tx)
	}

	merkleRoot := calcMerkleRoot(txs)
	if ntime == 0 {
		ntime = template.CurTime
	}
	header := btcwire.BlockHeader{
		Version:    template.Version,
		PrevBlock:  *prevHash,
		MerkleRoot: merkleRoot,
		Timestamp:  time.Unix(ntime, 0),
		Bits:       uint32(bits64),
		Nonce:      nonce,
	}

	block := btcwire.NewMsgBlock(&header)
	for _, tx := range txs {
		if err := block.AddTransaction(tx); err != nil {
			return nil, err
		}
	}
	hash := header.BlockHash()
	return &assembledWork{
		block:     block,
		coinbase:  coinbase,
		header:    header,
		blockHash: hash,
	}, nil
}

func buildCoinbaseTx(template *btcbtcjson.GetBlockTemplateResult, params *btcchaincfg.Params, rewardAddress string, extraNonce uint64) (*btcwire.MsgTx, error) {
	pkScript, err := btcPayToAddrScript(rewardAddress, params)
	if err != nil {
		return nil, fmt.Errorf("invalid btc lucky reward address: %w", err)
	}

	extraNonceBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(extraNonceBytes, extraNonce)
	sigScript, err := btctxscript.NewScriptBuilder().
		AddInt64(template.Height).
		AddData([]byte(coinbaseTag)).
		AddData(extraNonceBytes).
		Script()
	if err != nil {
		return nil, err
	}

	tx := btcwire.NewMsgTx(1)
	tx.AddTxIn(&btcwire.TxIn{
		PreviousOutPoint: btcwire.OutPoint{
			Index: ^uint32(0),
		},
		SignatureScript: sigScript,
		Sequence:        ^uint32(0),
	})
	tx.AddTxOut(btcwire.NewTxOut(*template.CoinbaseValue, pkScript))

	if template.DefaultWitnessCommitment != "" {
		commitment, err := hex.DecodeString(template.DefaultWitnessCommitment)
		if err != nil {
			return nil, fmt.Errorf("invalid default witness commitment: %w", err)
		}
		tx.TxIn[0].Witness = btcwire.TxWitness{make([]byte, 32)}
		tx.AddTxOut(btcwire.NewTxOut(0, commitment))
	}

	return tx, nil
}

func calcMerkleRoot(txs []*btcwire.MsgTx) btcchainhash.Hash {
	if len(txs) == 0 {
		return btcchainhash.Hash{}
	}
	hashes := make([]btcchainhash.Hash, len(txs))
	for i, tx := range txs {
		hashes[i] = tx.TxHash()
	}
	for len(hashes) > 1 {
		next := make([]btcchainhash.Hash, 0, (len(hashes)+1)/2)
		for i := 0; i < len(hashes); i += 2 {
			left := hashes[i]
			right := left
			if i+1 < len(hashes) {
				right = hashes[i+1]
			}
			next = append(next, hashPair(left, right))
		}
		hashes = next
	}
	return hashes[0]
}

func hashPair(left, right btcchainhash.Hash) btcchainhash.Hash {
	var data [64]byte
	copy(data[:32], left[:])
	copy(data[32:], right[:])
	first := sha256.Sum256(data[:])
	second := sha256.Sum256(first[:])
	var result btcchainhash.Hash
	copy(result[:], second[:])
	return result
}

func targetFromTemplate(template *btcbtcjson.GetBlockTemplateResult) (*big.Int, error) {
	if template.Target != "" {
		target, ok := new(big.Int).SetString(template.Target, 16)
		if !ok {
			return nil, fmt.Errorf("invalid template target %q", template.Target)
		}
		return target, nil
	}
	bits64, err := strconv.ParseUint(template.Bits, 16, 32)
	if err != nil {
		return nil, err
	}
	return btcblockchain.CompactToBig(uint32(bits64)), nil
}

func hashToBig(hash *btcchainhash.Hash) *big.Int {
	buf := hash.CloneBytes()
	for i := 0; i < len(buf)/2; i++ {
		buf[i], buf[len(buf)-1-i] = buf[len(buf)-1-i], buf[i]
	}
	return new(big.Int).SetBytes(buf)
}
