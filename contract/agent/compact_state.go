package agent

import (
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

// Fixed-order binary fields shared by persistence and state roots.
func writeCompactRuntimeSnapshot(e *contractcommon.StateEncoder, v RuntimeSnapshot) {
	e.Text(v.Address)
	e.Text(v.Subtype)
	e.U64(uint64(v.Version))
	writeCompactPredictionContract(e, v.Contract)
	writeCompactRuntimeState(e, v.State)
	raw, err := v.Managed.MarshalBinary()
	if err != nil && e.Err == nil {
		e.Err = err
	}
	e.Blob(raw)
	writeCompactRuntimeConfig(e, v.Config)
	writeCompactDeployPayloadHeader(e, v.Deploy)
}
func readCompactRuntimeSnapshot(d *contractcommon.StateDecoder) (v RuntimeSnapshot) {
	v.Address = d.Text()
	v.Subtype = d.Text()
	v.Version = d.U32()
	v.Contract = readCompactPredictionContract(d)
	v.State = readCompactRuntimeState(d)
	if d.Err == nil {
		d.Fail(v.Managed.UnmarshalBinary(d.Blob()))
	}
	v.Config = readCompactRuntimeConfig(d)
	v.Deploy = readCompactDeployPayloadHeader(d)
	return v
}

func writeCompactDeployPayloadHeader(e *contractcommon.StateEncoder, v DeployPayloadHeader) {
	e.I64(v.GasLimit)
	e.Text(v.Deployer)
	e.U64(v.DeployNonce)
	e.U64(uint64(v.Flags))
	e.Blob(v.ContentHash)
	e.U64(uint64(v.AgentVersion))
}
func readCompactDeployPayloadHeader(d *contractcommon.StateDecoder) (v DeployPayloadHeader) {
	v.GasLimit = d.I64()
	v.Deployer = d.Text()
	v.DeployNonce = d.U64()
	v.Flags = contractcommon.ContractFlags(d.U32())
	v.ContentHash = d.Blob()
	v.AgentVersion = d.U32()
	return v
}

func writeCompactRuntimeConfig(e *contractcommon.StateEncoder, v RuntimeConfig) {
	e.Text(v.CoreNodeAddress)
	e.Text(v.AgentAddress)
	e.Text(v.BootstrapAddress)
}
func readCompactRuntimeConfig(d *contractcommon.StateDecoder) (v RuntimeConfig) {
	v.CoreNodeAddress = d.Text()
	v.AgentAddress = d.Text()
	v.BootstrapAddress = d.Text()
	return v
}

func writeCompactRuntimeState(e *contractcommon.StateEncoder, v RuntimeState) {
	e.Text(v.Status)
	e.Bool(v.Closed)
	writeCompactPredictionRuntimeState(e, v.Prediction)
}
func readCompactRuntimeState(d *contractcommon.StateDecoder) (v RuntimeState) {
	v.Status = d.Text()
	v.Closed = d.Bool()
	v.Prediction = readCompactPredictionRuntimeState(d)
	return v
}

func writeCompactPredictionRuntimeState(e *contractcommon.StateEncoder, v PredictionRuntimeState) {
	e.Text(v.Status)
	e.U64(uint64(len(v.Bets)))
	for _, key := range contractcommon.SortedStateKeys(v.Bets) {
		e.Text(key)
		value := v.Bets[key]
		writeCompactPredictionBetRecord(e, value)
	}
	e.Text(v.GasBalance)
	e.U64(uint64(len(v.Confirmations)))
	for _, item := range v.Confirmations {
		writeCompactPredictionConfirmRecord(e, item)
	}
	e.U64(uint64(len(v.Rejections)))
	for _, item := range v.Rejections {
		writeCompactPredictionRejectRecord(e, item)
	}
}
func readCompactPredictionRuntimeState(d *contractcommon.StateDecoder) (v PredictionRuntimeState) {
	v.Status = d.Text()
	{
		count := d.CountEntries(4)
		if count > 0 {
			v.Bets = make(map[string]PredictionBetRecord, count)
		}
		previous := ""
		for i := 0; i < count && d.Err == nil; i++ {
			key := d.MapKey(previous, i)
			previous = key
			var value PredictionBetRecord
			value = readCompactPredictionBetRecord(d)
			v.Bets[key] = value
		}
	}
	v.GasBalance = d.Text()
	{
		count := d.CountEntries(8)
		if count > 0 {
			v.Confirmations = make([]PredictionConfirmRecord, count)
		}
		for i := 0; i < count && d.Err == nil; i++ {
			v.Confirmations[i] = readCompactPredictionConfirmRecord(d)
		}
	}
	{
		count := d.CountEntries(3)
		if count > 0 {
			v.Rejections = make([]PredictionRejectRecord, count)
		}
		for i := 0; i < count && d.Err == nil; i++ {
			v.Rejections[i] = readCompactPredictionRejectRecord(d)
		}
	}
	return v
}

func writeCompactPredictionBetRecord(e *contractcommon.StateEncoder, v PredictionBetRecord) {
	e.Text(v.Address)
	e.Text(v.OutcomeID)
	e.Text(v.Amount)
}
func readCompactPredictionBetRecord(d *contractcommon.StateDecoder) (v PredictionBetRecord) {
	v.Address = d.Text()
	v.OutcomeID = d.Text()
	v.Amount = d.Text()
	return v
}

func writeCompactPredictionConfirmRecord(e *contractcommon.StateEncoder, v PredictionConfirmRecord) {
	e.Text(v.Agent)
	e.Text(v.ResultType)
	e.Text(v.OutcomeID)
	e.Text(v.Result)
	e.Text(v.ResultURL)
	e.I64(v.ObservedAt)
	e.U64(uint64(v.AgentVersion))
	e.Text(v.ModelVersion)
}
func readCompactPredictionConfirmRecord(d *contractcommon.StateDecoder) (v PredictionConfirmRecord) {
	v.Agent = d.Text()
	v.ResultType = d.Text()
	v.OutcomeID = d.Text()
	v.Result = d.Text()
	v.ResultURL = d.Text()
	v.ObservedAt = d.I64()
	v.AgentVersion = d.U32()
	v.ModelVersion = d.Text()
	return v
}

func writeCompactPredictionRejectRecord(e *contractcommon.StateEncoder, v PredictionRejectRecord) {
	e.Text(v.Agent)
	e.Text(v.Reason)
	e.I64(v.CheckedAt)
}
func readCompactPredictionRejectRecord(d *contractcommon.StateDecoder) (v PredictionRejectRecord) {
	v.Agent = d.Text()
	v.Reason = d.Text()
	v.CheckedAt = d.I64()
	return v
}

func writeCompactPredictionContract(e *contractcommon.StateEncoder, v PredictionContract) {
	e.Text(v.Subtype)
	e.Text(v.Title)
	e.Text(v.Description)
	e.Text(v.TimeBase)
	e.I64(v.EventTime)
	e.I64(v.BetDeadline)
	e.I64(v.ConfirmAfter)
	e.Text(v.SourceURL)
	e.Text(v.BetAsset)
	e.Text(v.MinBetUnit)
	e.U64(uint64(len(v.Outcomes)))
	for _, item := range v.Outcomes {
		writeCompactPredictionOutcome(e, item)
	}
}
func readCompactPredictionContract(d *contractcommon.StateDecoder) (v PredictionContract) {
	v.Subtype = d.Text()
	v.Title = d.Text()
	v.Description = d.Text()
	v.TimeBase = d.Text()
	v.EventTime = d.I64()
	v.BetDeadline = d.I64()
	v.ConfirmAfter = d.I64()
	v.SourceURL = d.Text()
	v.BetAsset = d.Text()
	v.MinBetUnit = d.Text()
	{
		count := d.CountEntries(2)
		if count > 0 {
			v.Outcomes = make([]PredictionOutcome, count)
		}
		for i := 0; i < count && d.Err == nil; i++ {
			v.Outcomes[i] = readCompactPredictionOutcome(d)
		}
	}
	return v
}

func writeCompactPredictionOutcome(e *contractcommon.StateEncoder, v PredictionOutcome) {
	e.Text(v.ID)
	e.Text(v.Text)
}
func readCompactPredictionOutcome(d *contractcommon.StateDecoder) (v PredictionOutcome) {
	v.ID = d.Text()
	v.Text = d.Text()
	return v
}

const agentStoreHeader = "ASS\x01"
