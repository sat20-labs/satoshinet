package cpuminer

import "time"

type WorkerRange struct {
	WorkerID        int    `json:"worker_id"`
	ExtraNonceStart uint64 `json:"extra_nonce_start"`
	ExtraNonceEnd   uint64 `json:"extra_nonce_end"`
	MerkleRoot      string `json:"merkle_root"`
}

type JobRequest struct {
	Network       string `json:"network"`
	RewardAddress string `json:"reward_address"`
	MinerID       string `json:"miner_id"`
	Jobs          int    `json:"jobs"`
}

type CompactMiningJob struct {
	JobID             string        `json:"job_id"`
	TemplateID        string        `json:"template_id"`
	Network           string        `json:"network"`
	Height            int64         `json:"height"`
	PreviousBlockHash string        `json:"previous_block_hash"`
	Version           int32         `json:"version"`
	Bits              string        `json:"bits"`
	Target            string        `json:"target"`
	CurTime           int64         `json:"curtime"`
	MinTime           int64         `json:"mintime"`
	RewardAddress     string        `json:"reward_address"`
	MinerID           string        `json:"miner_id"`
	WorkerRanges      []WorkerRange `json:"worker_ranges"`
	ExpiresAt         time.Time     `json:"expires_at"`
}

type MiningSolution struct {
	JobID         string `json:"job_id"`
	TemplateID    string `json:"template_id"`
	Network       string `json:"network"`
	RewardAddress string `json:"reward_address"`
	WorkerID      int    `json:"worker_id"`
	ExtraNonce    uint64 `json:"extra_nonce"`
	NTime         int64  `json:"ntime"`
	Nonce         uint32 `json:"nonce"`
	HeaderHash    string `json:"header_hash"`
}

type FoundBlockRecord struct {
	BlockHash     string    `json:"blockHash"`
	BlockHeight   int64     `json:"blockHeight"`
	CoinbaseTxID  string    `json:"coinbaseTxid"`
	Vout          uint32    `json:"vout"`
	Amount        int64     `json:"amount"`
	RewardAddress string    `json:"rewardAddress"`
	JobID         string    `json:"jobId"`
	TemplateID    string    `json:"templateId"`
	Submitted     bool      `json:"submitted"`
	SubmitResult  string    `json:"submitResult"`
	CreatedAt     time.Time `json:"createdAt"`
}

type MiningJobBackend interface {
	Name() string
	Start() error
	Stop()
	IsReady() bool
	CurrentJob(req JobRequest) (*CompactMiningJob, error)
	SubmitSolution(solution *MiningSolution) (*FoundBlockRecord, error)
	Status() TemplateServiceStatus
}
