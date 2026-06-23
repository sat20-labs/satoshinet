package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type RuntimeConfig struct {
	CoreNodeAddress  string
	AgentAddress     string
	BootstrapAddress string
	AssetPrecision   contractframework.AssetPrecisionResolver `json:"-"`
	ChainParams      *chaincfg.Params                         `json:"-"`
}

type Runtime struct {
	address  ContractAddress
	deploy   DeployPayload
	deployer string
	contract PredictionContract
	config   RuntimeConfig
	state    RuntimeState
}

type RuntimeState struct {
	Status     string                 `json:"status"`
	Prediction PredictionRuntimeState `json:"prediction"`
}

type PredictionRuntimeState struct {
	Status        string                         `json:"status"`
	Bets          map[string]PredictionBetRecord `json:"bets,omitempty"`
	GasBalance    string                         `json:"gasBalance,omitempty"`
	Confirmations []PredictionConfirmRecord      `json:"confirmations,omitempty"`
	Rejections    []PredictionRejectRecord       `json:"rejections,omitempty"`
}

type PredictionBetRecord struct {
	Address   string `json:"address"`
	OutcomeID string `json:"outcome_id"`
	Amount    string `json:"amount"`
}

type PredictionConfirmRecord struct {
	Agent        string `json:"agent"`
	ResultType   string `json:"result_type"`
	OutcomeID    string `json:"outcome_id,omitempty"`
	Result       string `json:"result"`
	ResultURL    string `json:"result_url"`
	ObservedAt   int64  `json:"observed_at"`
	AgentVersion uint32 `json:"agent_version,omitempty"`
	ModelVersion string `json:"model_version,omitempty"`
}

type PredictionRejectRecord struct {
	Agent     string `json:"agent"`
	Reason    string `json:"reason"`
	CheckedAt int64  `json:"checked_at"`
}

func (s RuntimeState) Clone() RuntimeState {
	out := s
	out.Prediction = s.Prediction.Clone()
	return out
}

func (s PredictionRuntimeState) Clone() PredictionRuntimeState {
	out := s
	if s.Bets != nil {
		out.Bets = make(map[string]PredictionBetRecord, len(s.Bets))
		for k, v := range s.Bets {
			out.Bets[k] = v
		}
	}
	out.Confirmations = append([]PredictionConfirmRecord(nil), s.Confirmations...)
	out.Rejections = append([]PredictionRejectRecord(nil), s.Rejections...)
	return out
}

func predictionBetKey(address, outcomeID string) string {
	return strings.Join([]string{address, outcomeID}, "\x00")
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
	GasAmount string
	TimeValue int64
}

type ApplyConfirmRequest struct {
	Invoker   string
	Param     PredictionConfirmParam
	TimeValue int64
}

func NewRuntime(address ContractAddress, deploy DeployPayload, cfg RuntimeConfig) (*Runtime, error) {
	return NewRuntimeWithDeployer(address, deploy, cfg, "")
}

func NewRuntimeWithDeployer(address ContractAddress, deploy DeployPayload, cfg RuntimeConfig, deployer string) (*Runtime, error) {
	if deploy.SubType != SubtypePrediction {
		return nil, fmt.Errorf("unsupported agent subtype %s", deploy.SubType)
	}
	if deploy.Version != CurrentAgentVersion {
		return nil, fmt.Errorf("unsupported agent version %d", deploy.Version)
	}
	contract, err := DecodePredictionContract(deploy.ContractContent)
	if err != nil {
		return nil, fmt.Errorf("decode prediction contract: %w", err)
	}
	if err := contract.Check(); err != nil {
		return nil, err
	}
	if contract.Subtype != deploy.SubType {
		return nil, fmt.Errorf("agent subtype mismatch %s != %s", contract.Subtype, deploy.SubType)
	}
	return &Runtime{
		address:  address,
		deploy:   deploy,
		deployer: deployer,
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

func (r *Runtime) Clone() *Runtime {
	if r == nil {
		return nil
	}
	out := *r
	out.deploy.ContractContent = append([]byte(nil), r.deploy.ContractContent...)
	out.state = r.state.Clone()
	return &out
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
	r.addGasBalance(req.GasAmount)
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
	r.state.Prediction.Status = PredictionStatusConfirmed
	r.state.Prediction.Confirmations = append(r.state.Prediction.Confirmations, PredictionConfirmRecord{
		Agent:        req.Invoker,
		ResultType:   req.Param.ResultType,
		OutcomeID:    req.Param.OutcomeID,
		Result:       req.Param.Result,
		ResultURL:    req.Param.ResultURL,
		ObservedAt:   req.Param.ObservedAt,
		AgentVersion: req.Param.AgentVersion,
		ModelVersion: req.Param.ModelVersion,
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

func (r *Runtime) addGasBalance(amount string) {
	if amount == "" {
		return
	}
	next := decimalStringAdd(r.state.Prediction.GasBalance, amount)
	if parseDecimalOrZero(next).Sign() == 0 {
		r.state.Prediction.GasBalance = ""
		return
	}
	r.state.Prediction.GasBalance = next
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
