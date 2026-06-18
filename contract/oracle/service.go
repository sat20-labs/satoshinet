package oracle

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractnode "github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	DefaultAgentConfirmTxFee     = int64(10)
	DefaultAgentConfirmRetryBase = time.Minute
	DefaultAgentConfirmRetryMax  = 30 * time.Minute
)

type TipContext struct {
	Height int64
	Unix   int64
}

type LLMConfig struct {
	Provider    string
	Endpoint    string
	Model       string
	APIKey      string
	Timeout     time.Duration
	Temperature float64
	MaxTokens   int
}

type Config struct {
	DB          database.DB
	ChainParams *chaincfg.Params
	Interval    time.Duration

	LLM LLMConfig

	TipContext          func() (TipContext, error)
	CoreNodePubKey      func() ([]byte, error)
	SignCoreNodeMessage func([]byte) ([]byte, error)
	SubmitInvoke        func(contractcommon.ContractAddress, string, []byte) (*wire.MsgTx, error)

	Infof  func(string, ...interface{})
	Warnf  func(string, ...interface{})
	Debugf func(string, ...interface{})
}

type retryState struct {
	NextAttempt time.Time
	Failures    int
	LastReason  string
}

type Service struct {
	cfg    Config
	client agentcontract.LLMClient

	retryMu sync.Mutex
	retry   map[string]retryState
}

func NewService(cfg Config) (*Service, error) {
	client, err := newLLMClient(cfg.LLM, cfg.Infof)
	if err != nil {
		return nil, err
	}
	cfg.Interval = normalizeInterval(cfg.Interval)
	return &Service{
		cfg:    cfg,
		client: client,
		retry:  make(map[string]retryState),
	}, nil
}

func (s *Service) Enabled() bool {
	return s != nil && s.client != nil
}

func (s *Service) Run(quit <-chan struct{}) {
	if !s.Enabled() {
		return
	}
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	s.infof("Agent oracle started, interval=%s", s.cfg.Interval)
	for {
		if err := s.ProcessOnce(); err != nil {
			s.warnf("Agent oracle: %v", err)
		}
		select {
		case <-ticker.C:
		case <-quit:
			s.infof("Agent oracle stopped")
			return
		}
	}
}

func (s *Service) ProcessOnce() error {
	if !s.Enabled() || s.cfg.DB == nil {
		return nil
	}
	store := contractnode.NewAgentStateStore(s.cfg.DB)
	_, runtimeStore, err := store.LoadTip()
	if err != nil {
		return err
	}
	if runtimeStore == nil {
		return nil
	}
	if s.cfg.TipContext == nil {
		return fmt.Errorf("missing oracle tip context provider")
	}
	tip, err := s.cfg.TipContext()
	if err != nil {
		return err
	}
	corenodeAgent := agentcontract.NewPredictionAgent(s.client)
	if err := s.processReadyContracts(runtimeStore, corenodeAgent, tip.Unix); err != nil {
		return err
	}
	candidates, err := runtimeStore.PendingPredictionConfirms(tip.Height, tip.Unix)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		s.processConfirmCandidate(corenodeAgent, candidate, tip)
	}
	return nil
}

func (s *Service) processConfirmCandidate(corenodeAgent *agentcontract.PredictionAgent,
	candidate agentcontract.PredictionConfirmCandidate, tip TipContext) {

	contractAddr := candidate.Address.EncodeAddress()
	if !s.shouldAttempt(contractAddr, time.Now()) {
		s.debugf("Agent contract %s confirm skipped by retry backoff", contractAddr)
		return
	}
	corenodeAgent.Audit = func(event agentcontract.PredictionAgentAuditEvent) {
		s.audit(contractAddr, event)
	}
	observedAt := tip.Height
	if candidate.Contract.TimeBase == agentcontract.TimeBaseUnix {
		observedAt = tip.Unix
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.LLM.Timeout+30*time.Second)
	defer cancel()
	coreNodePubKey, err := s.coreNodePubKey()
	if err != nil {
		delay := s.recordFailure(contractAddr, err)
		s.warnf("Agent contract %s core node pubkey unavailable: %v, retry_after=%s",
			contractAddr, err, delay)
		return
	}
	param, err := corenodeAgent.BuildConfirmParam(ctx, agentcontract.PredictionAgentConfirmRequest{
		Contract:            candidate.Contract,
		ContractAddress:     candidate.Address,
		ResultURL:           candidate.Contract.SourceURL,
		ObservedAt:          observedAt,
		CoreNodePubKey:      coreNodePubKey,
		SignCoreNodeMessage: s.cfg.SignCoreNodeMessage,
		AgentVersion:        "satoshinet-agent-v1",
		ModelVersion:        s.cfg.LLM.Model,
	})
	if err != nil {
		delay := s.recordFailure(contractAddr, err)
		if errors.Is(err, agentcontract.ErrPredictionResultPending) {
			s.infof("Agent contract %s result pending, retry_after=%s", contractAddr, delay)
		} else {
			s.warnf("Agent contract %s confirm build failed: %v, retry_after=%s", contractAddr, err, delay)
		}
		return
	}
	tx, err := s.submitConfirm(candidate, param)
	if err != nil {
		delay := s.recordFailure(contractAddr, err)
		s.warnf("Agent contract %s confirm submit failed: %v, retry_after=%s", contractAddr, err, delay)
		return
	}
	s.clearFailure(contractAddr)
	s.infof("Agent contract %s confirm submitted: tx=%s result_type=%s outcome=%s result_url=%s result_hash=%s",
		contractAddr, tx.TxID(), param.ResultType, param.OutcomeID,
		param.ResultURL, param.ResultHash)
}

func (s *Service) processReadyContracts(runtimeStore *agentcontract.RuntimeStore,
	corenodeAgent *agentcontract.PredictionAgent, checkedAt int64) error {

	candidates, err := runtimeStore.PendingPredictionReady()
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		contractAddr := candidate.Address.EncodeAddress()
		if !s.shouldAttempt(contractAddr, time.Now()) {
			s.debugf("Agent contract %s ready skipped by retry backoff", contractAddr)
			continue
		}
		corenodeAgent.Audit = func(event agentcontract.PredictionAgentAuditEvent) {
			s.audit(contractAddr, event)
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.LLM.Timeout+30*time.Second)
		reject, ready, err := corenodeAgent.ReviewReady(ctx, agentcontract.PredictionAgentReadyReviewRequest{
			Contract:  candidate.Contract,
			CheckedAt: checkedAt,
		})
		cancel()
		if err != nil {
			delay := s.recordFailure(contractAddr, err)
			s.warnf("Agent contract %s ready review failed: %v, retry_after=%s", contractAddr, err, delay)
			continue
		}
		var tx *wire.MsgTx
		if ready {
			tx, err = s.submitReady(candidate)
		} else {
			tx, err = s.submitReject(candidate, reject)
		}
		if err != nil {
			delay := s.recordFailure(contractAddr, err)
			s.warnf("Agent contract %s ready transition submit failed: %v, retry_after=%s", contractAddr, err, delay)
			continue
		}
		s.clearFailure(contractAddr)
		if ready {
			s.infof("Agent contract %s ready submitted: tx=%s", contractAddr, tx.TxID())
		} else {
			s.infof("Agent contract %s reject submitted: tx=%s reason=%s",
				contractAddr, tx.TxID(), truncate(reject.Reason, 180))
		}
	}
	return nil
}

func (s *Service) submitReady(candidate agentcontract.PredictionReadyCandidate) (*wire.MsgTx, error) {
	return s.submitInvoke(candidate.Address, agentcontract.InvokeAPIReady, nil)
}

func (s *Service) submitReject(candidate agentcontract.PredictionReadyCandidate,
	param agentcontract.PredictionRejectParam) (*wire.MsgTx, error) {

	encoded, err := param.Encode()
	if err != nil {
		return nil, err
	}
	return s.submitInvoke(candidate.Address, agentcontract.InvokeAPIReject, encoded)
}

func (s *Service) submitConfirm(candidate agentcontract.PredictionConfirmCandidate,
	param agentcontract.PredictionConfirmParam) (*wire.MsgTx, error) {

	encoded, err := param.Encode()
	if err != nil {
		return nil, err
	}
	return s.submitInvoke(candidate.Address, agentcontract.InvokeAPIConfirm, encoded)
}

func (s *Service) submitInvoke(contract contractcommon.ContractAddress, action string,
	param []byte) (*wire.MsgTx, error) {

	if s.cfg.SubmitInvoke == nil {
		return nil, fmt.Errorf("missing oracle invoke submitter")
	}
	return s.cfg.SubmitInvoke(contract, action, param)
}

func (s *Service) coreNodePubKey() ([]byte, error) {
	if s.cfg.CoreNodePubKey == nil {
		return nil, fmt.Errorf("missing core node pubkey provider")
	}
	return s.cfg.CoreNodePubKey()
}

func (s *Service) shouldAttempt(contractAddr string, now time.Time) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	state, ok := s.retry[contractAddr]
	return !ok || !now.Before(state.NextAttempt)
}

func (s *Service) recordFailure(contractAddr string, err error) time.Duration {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	state := s.retry[contractAddr]
	state.Failures++
	delay := DefaultAgentConfirmRetryBase
	for i := 1; i < state.Failures && delay < DefaultAgentConfirmRetryMax; i++ {
		delay *= 2
	}
	if delay > DefaultAgentConfirmRetryMax {
		delay = DefaultAgentConfirmRetryMax
	}
	state.NextAttempt = time.Now().Add(delay)
	state.LastReason = err.Error()
	s.retry[contractAddr] = state
	return delay
}

func (s *Service) clearFailure(contractAddr string) {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	delete(s.retry, contractAddr)
}

func (s *Service) audit(contractAddr string, event agentcontract.PredictionAgentAuditEvent) {
	msg := truncate(event.Error, 180)
	reason := truncate(event.Reason, 180)
	s.infof("Agent contract %s audit stage=%s url=%s final_url=%s result_type=%s outcome=%s attempt=%d text_bytes=%d cleaned_bytes=%d candidates=%d result_hash=%s reason=%s error=%s",
		contractAddr, event.Stage, event.ResultURL, event.FinalURL, event.ResultType, event.OutcomeID,
		event.Attempt, event.TextBytes, event.CleanedBytes, event.CandidateCount, event.ResultHash, reason, msg)
}

func newLLMClient(cfg LLMConfig, infof func(string, ...interface{})) (agentcontract.LLMClient, error) {
	llmCfg := agentcontract.LLMConfig{
		Provider:    cfg.Provider,
		Endpoint:    cfg.Endpoint,
		Model:       cfg.Model,
		APIKey:      cfg.APIKey,
		Timeout:     cfg.Timeout,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
	}
	normalized, err := llmCfg.Normalized()
	if err != nil {
		return nil, err
	}
	client, err := agentcontract.NewLLMClient(normalized)
	if err != nil {
		return nil, err
	}
	if normalized.Provider == "" {
		if infof != nil {
			infof("Agent LLM access is disabled")
		}
		return nil, nil
	}
	if infof != nil {
		infof("Agent LLM access is enabled, provider=%s endpoint=%s model=%s",
			normalized.Provider, normalized.Endpoint, normalized.Model)
	}
	return client, nil
}

func normalizeInterval(interval time.Duration) time.Duration {
	if interval > 0 {
		return interval
	}
	return time.Minute
}

func truncate(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func (s *Service) infof(format string, args ...interface{}) {
	if s.cfg.Infof != nil {
		s.cfg.Infof(format, args...)
	}
}

func (s *Service) warnf(format string, args ...interface{}) {
	if s.cfg.Warnf != nil {
		s.cfg.Warnf(format, args...)
	}
}

func (s *Service) debugf(format string, args ...interface{}) {
	if s.cfg.Debugf != nil {
		s.cfg.Debugf(format, args...)
	}
}

func AgentRuntimeConfig(params *chaincfg.Params, agentPubKey, bootstrapPubKey []byte,
	pubKeyToAddress func([]byte, *chaincfg.Params) (string, error)) (agentcontract.RuntimeConfig, error) {

	if pubKeyToAddress == nil {
		return agentcontract.RuntimeConfig{}, fmt.Errorf("missing pubkey address converter")
	}
	agentAddress, err := pubKeyToAddress(agentPubKey, params)
	if err != nil {
		return agentcontract.RuntimeConfig{}, err
	}
	bootstrapAddress, err := pubKeyToAddress(bootstrapPubKey, params)
	if err != nil {
		return agentcontract.RuntimeConfig{}, err
	}
	return agentcontract.RuntimeConfig{
		CoreNodeAddress:           agentAddress,
		CoreNodePubKey:            hex.EncodeToString(agentPubKey),
		AgentAddress:              agentAddress,
		BootstrapAddress:          bootstrapAddress,
		ChainParams:               params,
		RequireConfirmAttestation: true,
	}, nil
}
