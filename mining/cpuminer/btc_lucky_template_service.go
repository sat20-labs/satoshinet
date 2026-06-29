package cpuminer

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	btcbtcjson "github.com/btcsuite/btcd/btcjson"
	btcbtcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcrpcclient "github.com/btcsuite/btcd/rpcclient"
)

type cachedBTCJob struct {
	job      *CompactMiningJob
	template *btcbtcjson.GetBlockTemplateResult
	params   *btcchaincfg.Params
}

type TemplateService struct {
	mu        sync.Mutex
	cfg       BTCLuckyTemplateServiceConfig
	params    *btcchaincfg.Params
	client    *btcrpcclient.Client
	running   bool
	lastTpl   *btcbtcjson.GetBlockTemplateResult
	lastTplAt time.Time
	jobs      map[string]*cachedBTCJob
	found     []FoundBlockRecord
	status    TemplateServiceStatus
}

func NewTemplateService(cfg BTCLuckyTemplateServiceConfig) (*TemplateService, error) {
	cfg.Normalize()
	params, err := BTCChainParams(cfg.Network)
	if err != nil {
		return nil, err
	}
	return &TemplateService{
		cfg:    cfg,
		params: params,
		jobs:   make(map[string]*cachedBTCJob),
		status: TemplateServiceStatus{
			Enabled:    cfg.Enabled,
			Backend:    cfg.Backend,
			BTCNetwork: cfg.Network,
		},
	}, nil
}

func (s *TemplateService) Name() string {
	return BTCLuckyBackendLocalTemplate
}

func (s *TemplateService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}
	client, err := btcrpcclient.New(&btcrpcclient.ConnConfig{
		Host:         s.cfg.RPCConnect,
		User:         s.cfg.RPCUser,
		Pass:         s.cfg.RPCPass,
		HTTPPostMode: true,
		DisableTLS:   s.cfg.RPCDisableTLS,
	}, nil)
	if err != nil {
		s.status.LastError = err.Error()
		return err
	}
	s.client = client
	s.running = true
	s.status.Running = true
	s.status.BTCRPCConnected = true
	return nil
}

func (s *TemplateService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		s.client.Shutdown()
		s.client = nil
	}
	s.running = false
	s.status.Running = false
	s.status.BTCRPCConnected = false
}

func (s *TemplateService) IsReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running && s.client != nil
}

func (s *TemplateService) CurrentJob(req JobRequest) (*CompactMiningJob, error) {
	if req.RewardAddress == "" {
		return nil, fmt.Errorf("missing btc lucky reward address")
	}
	if req.Workers < 1 {
		req.Workers = 1
	}

	s.mu.Lock()
	client := s.client
	running := s.running
	s.mu.Unlock()
	if !running || client == nil {
		return nil, fmt.Errorf("btc template service is not running")
	}

	now := time.Now()
	template, refreshed, err := s.currentTemplate(client, now)
	if err != nil {
		s.setTemplateError(err)
		return nil, err
	}
	if _, err := btcPayToAddrScript(req.RewardAddress, s.params); err != nil {
		s.setTemplateError(err)
		return nil, err
	}

	templateID := fmt.Sprintf("%s:%d:%s", template.PreviousHash, template.Height, template.WorkID)
	jobID := fmt.Sprintf("%s:%x", templateID, now.UnixNano())
	target := template.Target
	if target == "" {
		t, err := targetFromTemplate(template)
		if err != nil {
			s.setTemplateError(err)
			return nil, err
		}
		target = fmt.Sprintf("%064x", t)
	}
	workerRanges := makeWorkerRanges(req.Workers)
	for i := range workerRanges {
		work, err := assembleBTCWork(template, s.params, req.RewardAddress,
			workerRanges[i].ExtraNonceStart, 0, template.CurTime)
		if err != nil {
			s.setTemplateError(err)
			return nil, err
		}
		workerRanges[i].MerkleRoot = work.header.MerkleRoot.String()
	}
	job := &CompactMiningJob{
		JobID:             jobID,
		TemplateID:        templateID,
		Network:           s.cfg.Network,
		Height:            template.Height,
		PreviousBlockHash: template.PreviousHash,
		Version:           template.Version,
		Bits:              template.Bits,
		Target:            target,
		CurTime:           template.CurTime,
		MinTime:           template.MinTime,
		RewardAddress:     req.RewardAddress,
		WorkerRanges:      workerRanges,
		ExpiresAt:         now.Add(s.cfg.JobTTL),
	}

	s.mu.Lock()
	if refreshed {
		s.lastTpl = template
		s.lastTplAt = now
	}
	s.jobs[jobID] = &cachedBTCJob{job: job, template: template, params: s.params}
	s.pruneJobsLocked(now)
	if refreshed {
		s.status.LastTemplateTime = now
	}
	s.status.BTCHeight = template.Height
	s.status.TemplateCacheSize = 1
	s.status.ActiveJobs = len(s.jobs)
	s.status.LastError = ""
	s.mu.Unlock()

	return job, nil
}

func (s *TemplateService) currentTemplate(client *btcrpcclient.Client, now time.Time) (*btcbtcjson.GetBlockTemplateResult, bool, error) {
	s.mu.Lock()
	template := s.lastTpl
	fresh := template != nil && now.Sub(s.lastTplAt) < s.cfg.RefreshInterval
	s.mu.Unlock()
	if fresh {
		return template, false, nil
	}

	template, err := client.GetBlockTemplate(&btcbtcjson.TemplateRequest{
		Mode:  "template",
		Rules: []string{"segwit"},
	})
	if err != nil {
		return nil, false, err
	}
	return template, true, nil
}

func (s *TemplateService) SubmitSolution(solution *MiningSolution) (*FoundBlockRecord, error) {
	s.mu.Lock()
	cached := s.jobs[solution.JobID]
	client := s.client
	submit := s.cfg.SubmitBlock
	s.mu.Unlock()
	if cached == nil {
		return nil, fmt.Errorf("unknown btc lucky mining job %s", solution.JobID)
	}
	if solution.TemplateID != cached.job.TemplateID {
		return nil, fmt.Errorf("btc lucky mining solution template mismatch")
	}
	if solution.RewardAddress != cached.job.RewardAddress {
		return nil, fmt.Errorf("btc lucky mining solution reward address mismatch")
	}

	work, err := assembleBTCWork(cached.template, cached.params, solution.RewardAddress,
		solution.ExtraNonce, solution.Nonce, solution.NTime)
	if err != nil {
		s.setSubmitResult("", err)
		return nil, err
	}
	if work.blockHash.String() != solution.HeaderHash {
		err := fmt.Errorf("solution hash mismatch: got %s want %s", work.blockHash, solution.HeaderHash)
		s.setSubmitResult("", err)
		return nil, err
	}
	target, err := targetFromTemplate(cached.template)
	if err != nil {
		s.setSubmitResult("", err)
		return nil, err
	}
	if hashToBig(&work.blockHash).Cmp(target) > 0 {
		err := fmt.Errorf("solution does not satisfy target")
		s.setSubmitResult("", err)
		return nil, err
	}

	record := FoundBlockRecord{
		BlockHash:     work.blockHash.String(),
		BlockHeight:   cached.template.Height,
		CoinbaseTxID:  work.coinbase.TxHash().String(),
		Vout:          0,
		Amount:        work.coinbase.TxOut[0].Value,
		RewardAddress: solution.RewardAddress,
		JobID:         solution.JobID,
		TemplateID:    solution.TemplateID,
		CreatedAt:     time.Now(),
	}
	if submit {
		if client == nil {
			err := fmt.Errorf("btc rpc client is not connected")
			s.setSubmitResult("", err)
			return nil, err
		}
		var buf bytes.Buffer
		if err := work.block.Serialize(&buf); err != nil {
			s.setSubmitResult("", err)
			return nil, err
		}
		block, err := btcbtcutil.NewBlockFromBytes(buf.Bytes())
		if err != nil {
			s.setSubmitResult("", err)
			return nil, err
		}
		err = client.SubmitBlock(block, &btcbtcjson.SubmitBlockOptions{
			WorkID: cached.template.WorkID,
		})
		if err != nil {
			record.SubmitResult = err.Error()
			s.setSubmitResult(record.SubmitResult, err)
			s.rememberFound(record)
			return &record, err
		}
		record.Submitted = true
		record.SubmitResult = "accepted"
	} else {
		record.SubmitResult = "submit disabled"
	}

	s.setSubmitResult(record.SubmitResult, nil)
	s.rememberFound(record)
	return &record, nil
}

func (s *TemplateService) Status() TemplateServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Enabled = s.cfg.Enabled
	st.Backend = s.cfg.Backend
	st.BTCNetwork = s.cfg.Network
	st.Running = s.running
	st.BTCRPCConnected = s.client != nil && s.running
	st.ActiveJobs = len(s.jobs)
	if s.lastTpl != nil {
		st.BTCHeight = s.lastTpl.Height
		st.TemplateCacheSize = 1
	}
	return st
}

func (s *TemplateService) FoundBlocks() []FoundBlockRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FoundBlockRecord, len(s.found))
	copy(out, s.found)
	return out
}

func (s *TemplateService) pruneJobsLocked(now time.Time) {
	for id, job := range s.jobs {
		if now.After(job.job.ExpiresAt) {
			delete(s.jobs, id)
		}
	}
}

func (s *TemplateService) setTemplateError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.LastError = err.Error()
	}
}

func (s *TemplateService) setSubmitResult(result string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastSubmitTime = time.Now()
	s.status.LastSubmitResult = result
	if err != nil {
		s.status.LastError = err.Error()
	} else {
		s.status.LastError = ""
	}
}

func (s *TemplateService) rememberFound(record FoundBlockRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.found = append(s.found, record)
	if len(s.found) > 32 {
		s.found = s.found[len(s.found)-32:]
	}
}
