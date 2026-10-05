package dkvs

// ValidatePathSnapshotForClient validates a complete current network view.
// Only active records are admitted. Duplicates and control/deletion commands
// are rejected instead of being normalized into a winner. Source freshness
// remains the sync session's responsibility: a self-consistent signed set is
// not proof that it supersedes locally acknowledged state.
func ValidatePathSnapshotForClient(snapshot *PathSnapshot, opts RecordVerificationOptions) error {
	if snapshot == nil || snapshot.PathMeta == nil { return ErrInvalidSnapshot }
	path, meta := stringsTrimPath(snapshot.Path), snapshot.PathMeta
	if _, err := NormalizeActiveScope(ActiveScope{Prefix: path, Network: true}); err != nil || meta.Path != path || meta.Version != pathMetaVersion {
		return ErrInvalidSnapshot
	}
	if opts.Height != 0 && meta.ViewHeight > opts.Height { return ErrStaleEndpoint }
	if opts.Height == 0 { opts.Height = meta.ViewHeight }
	computed := &PathMeta{Version: pathMetaVersion, Path: path, Generation: meta.Generation, ViewHeight: meta.ViewHeight}
	seen := make(map[string]struct{})
	for _, record := range snapshot.Records {
		parsed, err := recordBelongsToPath(record, path)
		if err != nil || record.Flags != 0 { return ErrInvalidSnapshot }
		if _, duplicate := seen[record.Key]; duplicate { return ErrInvalidSnapshot }
		seen[record.Key] = struct{}{}
		if replicationMode(parsed, record) != ReplicationNetwork || isEndpointCacheRecord(record) { return ErrFreeLocalNotRelayable }
		size := uint64(RecordSize(record))
		if size > MaxNetworkSnapshotBytes-computed.ActiveTotalSize { return ErrBatchTooLarge }
		if IsExpired(record, meta.ViewHeight) { return ErrExpiredRecord }
		recordOpts := opts
		recordOpts.ExpectedKey = record.Key
		if err := VerifyRecordForClient(record, recordOpts); err != nil { return err }
		computed.ActiveRecords++
		computed.ActiveTotalSize += size
		xorPathMetaRoot(&computed.StateRoot, record)
		updateMinExpiry(computed, record)
	}
	if computed.StateRoot != meta.StateRoot || computed.ActiveRecords != meta.ActiveRecords ||
		computed.ActiveTotalSize != meta.ActiveTotalSize || computed.MinExpiryHeight != meta.MinExpiryHeight {
		return ErrPathDiverged
	}
	return nil
}
