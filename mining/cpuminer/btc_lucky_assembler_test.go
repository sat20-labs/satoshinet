package cpuminer

import (
	"bytes"
	"math/big"
	"testing"
	"time"

	btcbtcec "github.com/btcsuite/btcd/btcec/v2"
	btcbtcjson "github.com/btcsuite/btcd/btcjson"
	btcbtcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
)

func testTemplate() *btcbtcjson.GetBlockTemplateResult {
	coinbaseValue := int64(5000000000)
	return &btcbtcjson.GetBlockTemplateResult{
		Bits:          "207fffff",
		CurTime:       1700000000,
		Height:        1,
		PreviousHash:  "0000000000000000000000000000000000000000000000000000000000000000",
		Transactions:  nil,
		Version:       1,
		CoinbaseValue: &coinbaseValue,
		Target:        "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		WorkID:        "test-work",
	}
}

func testRewardAddress(t *testing.T) string {
	t.Helper()
	privKey, _ := btcbtcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	priv, err := btcbtcutil.NewWIF(privKey, &btcchaincfg.MainNetParams, true)
	if err != nil {
		t.Fatalf("NewWIF: %v", err)
	}
	addr, err := btcbtcutil.NewAddressPubKey(priv.PrivKey.PubKey().SerializeCompressed(), &btcchaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("NewAddressPubKey: %v", err)
	}
	return addr.EncodeAddress()
}

func TestResolveWorkerCount(t *testing.T) {
	n, err := ResolveWorkerCount("2", 0, 0)
	if err != nil {
		t.Fatalf("ResolveWorkerCount explicit: %v", err)
	}
	if n != 2 {
		t.Fatalf("explicit workers = %d, want 2", n)
	}

	n, err = ResolveWorkerCount("auto", 1<<30, 0)
	if err != nil {
		t.Fatalf("ResolveWorkerCount auto: %v", err)
	}
	if n != 1 {
		t.Fatalf("auto with huge reserve = %d, want 1", n)
	}

	n, err = ResolveWorkerCount("auto", 0, 1)
	if err != nil {
		t.Fatalf("ResolveWorkerCount max: %v", err)
	}
	if n != 1 {
		t.Fatalf("auto with max = %d, want 1", n)
	}

	if _, err := ResolveWorkerCount("0", 0, 0); err == nil {
		t.Fatalf("expected zero workers to fail")
	}
}

func TestMakeWorkerRanges(t *testing.T) {
	ranges := makeWorkerRanges(4)
	if len(ranges) != 4 {
		t.Fatalf("range count = %d, want 4", len(ranges))
	}
	for i, r := range ranges {
		if r.WorkerID != i {
			t.Fatalf("worker id = %d, want %d", r.WorkerID, i)
		}
		if r.ExtraNonceStart != r.ExtraNonceEnd {
			t.Fatalf("range %d is not a compact fixed extranonce range", i)
		}
	}
}

func TestAssembleBTCWorkUsesBitcoinWireSerialization(t *testing.T) {
	rewardAddr := testRewardAddress(t)
	work, err := assembleBTCWork(testTemplate(), &btcchaincfg.MainNetParams, rewardAddr, 7, 11, 1700000001)
	if err != nil {
		t.Fatalf("assembleBTCWork: %v", err)
	}
	if len(work.block.Transactions) != 1 {
		t.Fatalf("tx count = %d, want 1", len(work.block.Transactions))
	}
	if got := work.coinbase.TxOut[0].Value; got != 5000000000 {
		t.Fatalf("coinbase value = %d", got)
	}

	var txBuf bytes.Buffer
	if err := work.coinbase.Serialize(&txBuf); err != nil {
		t.Fatalf("serialize coinbase: %v", err)
	}
	decoded, err := btcbtcutil.NewTxFromBytes(txBuf.Bytes())
	if err != nil {
		t.Fatalf("decode serialized coinbase as btc tx: %v", err)
	}
	if decoded.MsgTx().TxOut[0].Value != work.coinbase.TxOut[0].Value {
		t.Fatalf("decoded coinbase value mismatch")
	}

	var blockBuf bytes.Buffer
	if err := work.block.Serialize(&blockBuf); err != nil {
		t.Fatalf("serialize block: %v", err)
	}
	if _, err := btcbtcutil.NewBlockFromBytes(blockBuf.Bytes()); err != nil {
		t.Fatalf("decode serialized block as btc block: %v", err)
	}
}

func TestCompactJobHeaderHashMatchesAssembledWork(t *testing.T) {
	tpl := testTemplate()
	rewardAddr := testRewardAddress(t)
	work, err := assembleBTCWork(tpl, &btcchaincfg.MainNetParams, rewardAddr, 7, 11, tpl.CurTime)
	if err != nil {
		t.Fatalf("assembleBTCWork: %v", err)
	}
	job := &CompactMiningJob{
		PreviousBlockHash: tpl.PreviousHash,
		Version:           tpl.Version,
		Bits:              tpl.Bits,
		CurTime:           tpl.CurTime,
	}
	hash, err := hashCompactJobHeader(job, WorkerRange{
		WorkerID:        0,
		ExtraNonceStart: 7,
		ExtraNonceEnd:   7,
		MerkleRoot:      work.header.MerkleRoot.String(),
	}, 11)
	if err != nil {
		t.Fatalf("hashCompactJobHeader: %v", err)
	}
	if hash != work.blockHash {
		t.Fatalf("compact hash = %s, want %s", hash, work.blockHash)
	}
}

func TestTargetFromTemplate(t *testing.T) {
	target, err := targetFromTemplate(testTemplate())
	if err != nil {
		t.Fatalf("targetFromTemplate: %v", err)
	}
	want, _ := new(big.Int).SetString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)
	if target.Cmp(want) != 0 {
		t.Fatalf("target = %x, want %x", target, want)
	}

	tpl := testTemplate()
	tpl.Target = ""
	target, err = targetFromTemplate(tpl)
	if err != nil {
		t.Fatalf("targetFromTemplate bits: %v", err)
	}
	if target.Sign() <= 0 {
		t.Fatalf("target from bits must be positive")
	}
}

func TestTemplateServiceSubmitSolutionRecordsMetadataWithoutSubmit(t *testing.T) {
	tpl := testTemplate()
	rewardAddr := testRewardAddress(t)
	service, err := NewTemplateService(BTCLuckyTemplateServiceConfig{
		Enabled:     true,
		Network:     "mainnet",
		SubmitBlock: false,
	})
	if err != nil {
		t.Fatalf("NewTemplateService: %v", err)
	}

	job := &CompactMiningJob{
		JobID:             "job-1",
		TemplateID:        "template-1",
		Network:           "mainnet",
		Height:            tpl.Height,
		PreviousBlockHash: tpl.PreviousHash,
		Version:           tpl.Version,
		Bits:              tpl.Bits,
		CurTime:           tpl.CurTime,
		Target:            tpl.Target,
		RewardAddress:     rewardAddr,
		ExpiresAt:         time.Now().Add(time.Minute),
	}
	work, err := assembleBTCWork(tpl, &btcchaincfg.MainNetParams, rewardAddr, 1, 2, tpl.CurTime)
	if err != nil {
		t.Fatalf("assembleBTCWork: %v", err)
	}

	service.jobs[job.JobID] = &cachedBTCJob{
		job:      job,
		template: tpl,
		params:   &btcchaincfg.MainNetParams,
	}
	record, err := service.SubmitSolution(&MiningSolution{
		JobID:         job.JobID,
		TemplateID:    job.TemplateID,
		Network:       job.Network,
		RewardAddress: rewardAddr,
		ExtraNonce:    1,
		NTime:         tpl.CurTime,
		Nonce:         2,
		HeaderHash:    work.blockHash.String(),
	})
	if err != nil {
		t.Fatalf("SubmitSolution: %v", err)
	}
	if record.BlockHash != work.blockHash.String() {
		t.Fatalf("record block hash = %s, want %s", record.BlockHash, work.blockHash)
	}
	if record.SubmitResult != "submit disabled" {
		t.Fatalf("submit result = %q", record.SubmitResult)
	}
	if len(service.FoundBlocks()) != 1 {
		t.Fatalf("found block count = %d, want 1", len(service.FoundBlocks()))
	}
}
