package cpuminer

import (
	"sync"
	"time"
)

type MinerStatus struct {
	Enabled          bool               `json:"enabled"`
	Running          bool               `json:"running"`
	Backend          string             `json:"backend"`
	RewardAddress    string             `json:"rewardAddress"`
	MinerID          string             `json:"minerId"`
	Jobs             int                `json:"jobs"`
	JobsMode         string             `json:"jobsMode"`
	LowPriority      bool               `json:"lowPriority"`
	LowPrioritySleep string             `json:"lowPrioritySleep"`
	HashesPerSecond  float64            `json:"hashesPerSecond"`
	BestShare        string             `json:"bestShare"`
	CurrentTarget    string             `json:"currentTarget"`
	JobID            string             `json:"jobId"`
	BTCHeight        int64              `json:"btcHeight"`
	LastJobTime      time.Time          `json:"lastJobTime"`
	LastSubmitTime   time.Time          `json:"lastSubmitTime"`
	LastSubmitResult string             `json:"lastSubmitResult"`
	LastError        string             `json:"lastError"`
	FoundBlocks      []FoundBlockRecord `json:"foundBlocks,omitempty"`
}

type TemplateServiceStatus struct {
	Enabled           bool      `json:"enabled"`
	Running           bool      `json:"running"`
	Backend           string    `json:"backend"`
	BTCRPCConnected   bool      `json:"btcRpcConnected"`
	BTCNetwork        string    `json:"btcNetwork"`
	BTCHeight         int64     `json:"btcHeight"`
	TemplateCacheSize int       `json:"templateCacheSize"`
	ActiveJobs        int       `json:"activeJobs"`
	LastTemplateTime  time.Time `json:"lastTemplateTime"`
	LastSubmitTime    time.Time `json:"lastSubmitTime"`
	LastSubmitResult  string    `json:"lastSubmitResult"`
	LastError         string    `json:"lastError"`
}

type speedMonitor struct {
	mu          sync.Mutex
	windowStart time.Time
	hashes      uint64
	hps         float64
}

func newSpeedMonitor() *speedMonitor {
	return &speedMonitor{windowStart: time.Now()}
}

func (s *speedMonitor) Add(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hashes += n
	now := time.Now()
	elapsed := now.Sub(s.windowStart)
	if elapsed >= time.Second {
		s.hps = float64(s.hashes) / elapsed.Seconds()
		s.hashes = 0
		s.windowStart = now
	}
}

func (s *speedMonitor) HashesPerSecond() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hps
}
