package dkvs

// Small direct-read responses account for both current record bytes and their
// key-state projection. Multi-page synchronization uses ActivePage instead.
func prefixKeyStateSize(state DKVSKeyState) int {
	return len(state.Key) + len(state.ETag) + 128
}

func appendPrefixKeyState(states *[]DKVSKeyState, totalBytes *int, state DKVSKeyState) error {
	if state.Status != KeyStateActive { return ErrInvalidRecord }
	if len(*states) >= MaxPrefixReadRecords { return ErrBatchTooLarge }
	size := prefixKeyStateSize(state)
	if size > MaxPrefixReadBytes-*totalBytes { return ErrBatchTooLarge }
	*totalBytes += size
	*states = append(*states, state)
	return nil
}
