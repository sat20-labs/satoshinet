package agent

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

type RuntimeConfig struct {
	CoreNodeAddress           string
	CoreNodePubKey            string
	AgentAddress              string
	BootstrapAddress          string
	ChainParams               *chaincfg.Params `json:"-"`
	RequireConfirmAttestation bool
}

type Runtime struct {
	address  ContractAddress
	deploy   DeployPayload
	contract PredictionContract
	config   RuntimeConfig
	state    RuntimeState
}

type ApplyReadyRequest struct {
	Invoker string
}

type ApplyRejectRequest struct {
	Invoker string
	Param   PredictionRejectParam
}

type ApplyBetRequest struct {
	Invoker   string
	Param     PredictionBetParam
	AssetName string
	Amount    string
	TimeValue int64
}

type ApplyConfirmRequest struct {
	Invoker   string
	Param     PredictionConfirmParam
	TimeValue int64
}

func NewRuntime(address ContractAddress, deploy DeployPayload, cfg RuntimeConfig) (*Runtime, error) {
	if deploy.Subtype != SubtypePrediction {
		return nil, fmt.Errorf("unsupported agent subtype %s", deploy.Subtype)
	}
	if deploy.AgentVersion != CurrentAgentVersion {
		return nil, fmt.Errorf("unsupported agent version %d", deploy.AgentVersion)
	}
	contract, err := DecodePredictionContract(deploy.ContractContent)
	if err != nil {
		return nil, fmt.Errorf("decode prediction contract: %w", err)
	}
	if err := contract.Check(); err != nil {
		return nil, err
	}
	if contract.Subtype != deploy.Subtype {
		return nil, fmt.Errorf("agent subtype mismatch %s != %s", contract.Subtype, deploy.Subtype)
	}
	return &Runtime{
		address:  address,
		deploy:   deploy,
		contract: contract,
		config:   cfg,
		state: RuntimeState{
			Status: StatusPendingReady,
			Prediction: PredictionRuntimeState{
				Status: PredictionStatusPendingResult,
				Bets:   make(map[string]PredictionBetRecord),
			},
		},
	}, nil
}

func (r *Runtime) Address() ContractAddress {
	return r.address
}

func (r *Runtime) Contract() PredictionContract {
	return r.contract
}

func (r *Runtime) State() RuntimeState {
	return r.state.Clone()
}

func (r *Runtime) StateJSON() ([]byte, error) {
	return json.Marshal(r.state)
}

func (r *Runtime) LoadStateJSON(data []byte) error {
	var state RuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Prediction.Bets == nil {
		state.Prediction.Bets = make(map[string]PredictionBetRecord)
	}
	r.state = state
	return nil
}

func (r *Runtime) ApplyReady(req ApplyReadyRequest) error {
	if err := r.requireCoreNode(req.Invoker); err != nil {
		return err
	}
	if r.state.Status != StatusPendingReady {
		return fmt.Errorf("agent contract is not pending ready")
	}
	r.state.Status = StatusReady
	r.state.Prediction.Status = PredictionStatusBetting
	return nil
}

func (r *Runtime) ApplyReject(req ApplyRejectRequest) error {
	if err := r.requireCoreNode(req.Invoker); err != nil {
		return err
	}
	if r.state.Status != StatusPendingReady {
		return fmt.Errorf("agent contract is not pending ready")
	}
	if err := req.Param.Check(); err != nil {
		return err
	}
	r.state.Status = StatusRejected
	r.state.Prediction.Status = PredictionStatusRejected
	r.state.Prediction.Rejections = append(r.state.Prediction.Rejections, PredictionRejectRecord{
		Agent:     req.Invoker,
		Reason:    req.Param.Reason,
		CheckedAt: req.Param.CheckedAt,
	})
	return nil
}

func (r *Runtime) ApplyBet(req ApplyBetRequest) error {
	if r.state.Status != StatusReady {
		return fmt.Errorf("agent contract is not ready")
	}
	if r.state.Prediction.Status != PredictionStatusBetting {
		return fmt.Errorf("prediction contract is not accepting bets")
	}
	if req.TimeValue > r.contract.BetDeadline {
		return fmt.Errorf("prediction bet deadline passed")
	}
	if req.AssetName != r.contract.BetAsset {
		return fmt.Errorf("prediction bet asset mismatch %s != %s", req.AssetName, r.contract.BetAsset)
	}
	if err := req.Param.Check(r.contract); err != nil {
		return err
	}
	if err := CheckBetAmount(req.Amount, r.contract.MinBetUnit); err != nil {
		return err
	}
	if req.Invoker == "" {
		return fmt.Errorf("prediction bet invoker is empty")
	}
	r.addBet(req.Invoker, req.Param.OutcomeID, req.Amount)
	return nil
}

func (r *Runtime) AdvancePredictionStatus(timeValue int64) bool {
	if r == nil || r.state.Status != StatusReady {
		return false
	}
	changed := false
	if r.state.Prediction.Status == PredictionStatusBetting && timeValue > r.contract.BetDeadline {
		r.state.Prediction.Status = PredictionStatusClosedForBet
		changed = true
	}
	if r.state.Prediction.Status == PredictionStatusClosedForBet && timeValue >= r.contract.ConfirmAfter {
		r.state.Prediction.Status = PredictionStatusPendingResult
		changed = true
	}
	return changed
}

func (r *Runtime) ApplyConfirm(req ApplyConfirmRequest) (*PredictionSettlementPlan, error) {
	if err := r.requireCoreNode(req.Invoker); err != nil {
		return nil, err
	}
	if r.state.Status != StatusReady {
		return nil, fmt.Errorf("agent contract is not ready")
	}
	if req.TimeValue <= r.contract.BetDeadline {
		return nil, fmt.Errorf("prediction betting is still open")
	}
	if req.TimeValue < r.contract.ConfirmAfter {
		return nil, fmt.Errorf("prediction confirm_after has not been reached")
	}
	if err := req.Param.Check(r.contract); err != nil {
		return nil, err
	}
	if r.config.RequireConfirmAttestation || r.config.CoreNodePubKey != "" ||
		req.Param.CoreNodePubKey != "" || req.Param.CoreNodeSignature != "" {
		if err := VerifyPredictionConfirmAttestation(r.address, req.Param,
			r.config.CoreNodePubKey, r.config.CoreNodeAddress, r.config.ChainParams); err != nil {
			return nil, err
		}
	}
	r.state.Prediction.Status = PredictionStatusConfirmed
	r.state.Prediction.Confirmations = append(r.state.Prediction.Confirmations, PredictionConfirmRecord{
		Agent:             req.Invoker,
		ResultType:        req.Param.ResultType,
		OutcomeID:         req.Param.OutcomeID,
		SourceURL:         req.Param.SourceURL,
		ResultURL:         req.Param.ResultURL,
		ResultHash:        req.Param.ResultHash,
		ObservedAt:        req.Param.ObservedAt,
		AgentVersion:      req.Param.AgentVersion,
		ModelVersion:      req.Param.ModelVersion,
		CoreNodePubKey:    req.Param.CoreNodePubKey,
		CoreNodeSignature: req.Param.CoreNodeSignature,
	})
	plan, err := r.buildSettlementPlan(req.Param)
	if err != nil {
		return nil, err
	}
	r.state.Status = StatusCompleted
	r.state.Prediction.Status = PredictionStatusSettled
	return plan, nil
}

func (r *Runtime) addBet(address, outcomeID, amount string) {
	if r.state.Prediction.Bets == nil {
		r.state.Prediction.Bets = make(map[string]PredictionBetRecord)
	}
	key := predictionBetKey(address, outcomeID)
	record := r.state.Prediction.Bets[key]
	if record.Address == "" {
		record.Address = address
		record.OutcomeID = outcomeID
	}
	record.Amount = decimalStringAdd(record.Amount, amount)
	r.state.Prediction.Bets[key] = record
}

func (r *Runtime) requireCoreNode(invoker string) error {
	if r.config.CoreNodeAddress == "" {
		return errors.New("missing core node address")
	}
	if invoker != r.config.CoreNodeAddress {
		return fmt.Errorf("invoker is not core node")
	}
	return nil
}
