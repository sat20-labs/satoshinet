package agent

type PredictionConfirmCandidate struct {
	Address  ContractAddress
	Contract PredictionContract
	State    RuntimeState
}

type PredictionReadyCandidate struct {
	Address  ContractAddress
	Contract PredictionContract
	State    RuntimeState
}

func (s *RuntimeStore) PendingPredictionReady() ([]PredictionReadyCandidate, error) {
	snapshots, err := s.Snapshots()
	if err != nil {
		return nil, err
	}
	out := make([]PredictionReadyCandidate, 0)
	for _, snapshot := range snapshots {
		if snapshot.Subtype != SubtypePrediction || snapshot.State.Status != StatusPendingReady {
			continue
		}
		address, err := DecodeContractAddress(snapshot.Address)
		if err != nil {
			return nil, err
		}
		out = append(out, PredictionReadyCandidate{
			Address:  address,
			Contract: snapshot.Contract,
			State:    snapshot.State,
		})
	}
	return out, nil
}

func (s *RuntimeStore) PendingPredictionConfirms(heightValue, unixValue int64) ([]PredictionConfirmCandidate, error) {
	snapshots, err := s.Snapshots()
	if err != nil {
		return nil, err
	}
	out := make([]PredictionConfirmCandidate, 0)
	for _, snapshot := range snapshots {
		if snapshot.Subtype != SubtypePrediction {
			continue
		}
		contract := snapshot.Contract
		timeValue := heightValue
		if contract.TimeBase == TimeBaseUnix {
			timeValue = unixValue
		}
		if timeValue < contract.ConfirmAfter {
			continue
		}
		state := snapshot.State
		if state.Status != StatusReady {
			continue
		}
		switch state.Prediction.Status {
		case PredictionStatusSettled, PredictionStatusConfirmed:
			continue
		}
		address, err := DecodeContractAddress(snapshot.Address)
		if err != nil {
			return nil, err
		}
		out = append(out, PredictionConfirmCandidate{
			Address:  address,
			Contract: contract,
			State:    state,
		})
	}
	return out, nil
}
