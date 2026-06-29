package cpuminer

import (
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"time"

	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
)

const (
	lowPriorityYieldEvery = uint64(4096)
	lowPrioritySleepEvery = uint64(65536)
	lowPrioritySleep      = time.Millisecond
)

type Miner struct {
	mu      sync.Mutex
	cfg     BTCLuckyMinerConfig
	backend MiningJobBackend
	workers int
	speed   *speedMonitor
	status  MinerStatus
	quit    chan struct{}
	wg      sync.WaitGroup
	running bool
}

func NewMiner(cfg BTCLuckyMinerConfig, backend MiningJobBackend) (*Miner, error) {
	cfg.Normalize()
	if backend == nil {
		return nil, fmt.Errorf("btc lucky mining backend is required")
	}
	if cfg.RewardAddr == "" {
		return nil, fmt.Errorf("btc lucky mining reward address is required")
	}
	workers, err := ResolveWorkerCount(cfg.Workers, cfg.ReserveCores, cfg.MaxWorkers)
	if err != nil {
		return nil, err
	}
	return &Miner{
		cfg:     cfg,
		backend: backend,
		workers: workers,
		speed:   newSpeedMonitor(),
		status: MinerStatus{
			Enabled:       cfg.Enabled,
			Backend:       cfg.Backend,
			RewardAddress: cfg.RewardAddr,
			Workers:       workers,
			WorkersMode:   cfg.Workers,
			LowPriority:   cfg.LowPriority,
		},
	}, nil
}

func (m *Miner) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.quit = make(chan struct{})
	m.running = true
	m.status.Running = true
	m.mu.Unlock()

	if !m.backend.IsReady() {
		if err := m.backend.Start(); err != nil {
			m.setError(err)
			m.mu.Lock()
			m.running = false
			m.status.Running = false
			m.mu.Unlock()
			return err
		}
	}

	m.wg.Add(1)
	go m.controller()
	log.Infof("BTC lucky miner started with %d workers", m.workers)
	return nil
}

func (m *Miner) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	close(m.quit)
	m.running = false
	m.status.Running = false
	m.mu.Unlock()

	m.wg.Wait()
	log.Infof("BTC lucky miner stopped")
}

func (m *Miner) IsMining() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Miner) HashesPerSecond() float64 {
	return m.speed.HashesPerSecond()
}

func (m *Miner) Status() MinerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.status
	st.HashesPerSecond = m.speed.HashesPerSecond()
	if backend, ok := m.backend.(interface {
		FoundBlocks() []FoundBlockRecord
	}); ok {
		st.FoundBlocks = backend.FoundBlocks()
	}
	return st
}

func (m *Miner) controller() {
	defer m.wg.Done()

	for {
		select {
		case <-m.quit:
			return
		default:
		}

		job, err := m.backend.CurrentJob(JobRequest{
			Network:       m.cfg.Network,
			RewardAddress: m.cfg.RewardAddr,
			Workers:       m.workers,
		})
		if err != nil {
			m.setError(err)
			if !sleepOrDone(m.quit, 10*time.Second) {
				return
			}
			continue
		}
		m.setJob(job)
		m.mineJob(job)
	}
}

func (m *Miner) mineJob(job *CompactMiningJob) {
	var workerWg sync.WaitGroup
	for _, r := range job.WorkerRanges {
		workerRange := r
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			m.mineRange(job, workerRange)
		}()
	}

	done := make(chan struct{})
	go func() {
		workerWg.Wait()
		close(done)
	}()

	select {
	case <-m.quit:
		workerWg.Wait()
	case <-done:
	}
}

func (m *Miner) mineRange(job *CompactMiningJob, r WorkerRange) {
	target, ok := new(big.Int).SetString(job.Target, 16)
	if !ok {
		m.setError(fmt.Errorf("invalid btc lucky target %q", job.Target))
		return
	}

	for extraNonce := r.ExtraNonceStart; extraNonce <= r.ExtraNonceEnd; extraNonce++ {
		for nonce := uint32(0); ; nonce++ {
			select {
			case <-m.quit:
				return
			default:
			}
			if time.Now().After(job.ExpiresAt) {
				return
			}

			hash, err := m.currentWork(job, r, nonce)
			if err != nil {
				m.setError(err)
				return
			}
			m.speed.Add(1)
			m.lowPriorityPause(uint64(nonce))
			m.updateBestShare(hash.String())

			if hashToBig(hash).Cmp(target) <= 0 {
				solution := &MiningSolution{
					JobID:         job.JobID,
					TemplateID:    job.TemplateID,
					Network:       job.Network,
					RewardAddress: job.RewardAddress,
					WorkerID:      r.WorkerID,
					ExtraNonce:    extraNonce,
					NTime:         job.CurTime,
					Nonce:         nonce,
					HeaderHash:    hash.String(),
				}
				record, err := m.backend.SubmitSolution(solution)
				if err != nil {
					m.setSubmit(err.Error())
					return
				}
				m.setSubmit(record.SubmitResult)
				return
			}

			if nonce == ^uint32(0) {
				break
			}
		}
		if extraNonce == ^uint64(0) {
			return
		}
	}
}

func (m *Miner) lowPriorityPause(nonce uint64) {
	if nonce%lowPriorityYieldEvery == 0 {
		runtime.Gosched()
	}
	if m.cfg.LowPriority && nonce%lowPrioritySleepEvery == 0 {
		time.Sleep(lowPrioritySleep)
	}
}

func (m *Miner) currentWork(job *CompactMiningJob, r WorkerRange, nonce uint32) (*btcchainhash.Hash, error) {
	hash, err := hashCompactJobHeader(job, r, nonce)
	if err != nil {
		return nil, err
	}
	return &hash, nil
}

func (m *Miner) setJob(job *CompactMiningJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.JobID = job.JobID
	m.status.CurrentTarget = job.Target
	m.status.BTCHeight = job.Height
	m.status.LastJobTime = time.Now()
	m.status.LastError = ""
}

func (m *Miner) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.status.LastError = err.Error()
	}
}

func (m *Miner) updateBestShare(hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.BestShare == "" || hash < m.status.BestShare {
		m.status.BestShare = hash
	}
}

func (m *Miner) setSubmit(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.LastSubmitTime = time.Now()
	m.status.LastSubmitResult = result
}

func sleepOrDone(done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return false
	case <-timer.C:
		return true
	}
}
