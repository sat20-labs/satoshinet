package dkvs

// ValidateBatchEndpointID enforces endpoint affinity for v1 FREE_LOCAL CAS
// requests. Local-only records are scoped to exactly one service node and must
// never be accepted after a client switches to a different endpoint.
func (i *Indexer) ValidateBatchEndpointID(mutations []CASMutation, endpointID string) error {
	if i == nil || len(mutations) == 0 {
		return ErrInvalidRecord
	}
	hasLocalOnly := false
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return ErrInvalidRecord
		}
		if isFreeLocalRecord(mutation.Record) {
			hasLocalOnly = true
		}
	}
	if !hasLocalOnly {
		return nil
	}
	expected := i.EndpointID()
	if expected == "" {
		return ErrStaleEndpoint
	}
	if endpointID == "" || endpointID != expected {
		return ErrLocalOnlyEndpointMismatch
	}
	return nil
}
