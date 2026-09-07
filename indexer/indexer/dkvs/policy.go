package dkvs

import "github.com/sat20-labs/satoshinet/wire"

const (
	// DKVS TTL is measured in SatoshiNet block heights. These defaults keep
	// the previous product retention windows assuming the nominal 12-second
	// block cadence, without reintroducing wall-clock time into record state.
	DefaultMailboxMaxMsgTTLBlocks   = uint64(30 * 24 * 60 * 60 / 12)
	DefaultMailboxMaxShareTTLBlocks = uint64(365 * 24 * 60 * 60 / 12)
	DefaultTmpMaxTTLBlocks          = uint64(24 * 60 * 60 / 12)

	DefaultMailboxMaxMsgBytes          = uint64(8 * 1024 * 1024)
	DefaultMailboxMaxMessages          = uint64(1024)
	DefaultMailboxMaxMsgBytesPerSender = uint64(2 * 1024 * 1024)
	DefaultMailboxMaxMessagesPerSender = uint64(128)
	DefaultMailboxMaxMsgSize           = wire.MaxDKVSBlobValueSize
	DefaultMailboxMaxMsgTTL            = DefaultMailboxMaxMsgTTLBlocks
	DefaultMailboxMaxShareBytes        = uint64(1024 * 1024)
	DefaultMailboxMaxShares            = uint64(256)
	DefaultMailboxMaxShareSize         = MaxRecordValueSize
	DefaultMailboxMaxShareTTL          = DefaultMailboxMaxShareTTLBlocks

	DefaultBlobMaxValueSize              = wire.MaxDKVSBlobValueSize
	DefaultBlobMaxFreeLocalKeysPerSigner = uint64(1)

	DefaultTmpMaxTTL  = DefaultTmpMaxTTLBlocks
	DefaultTmpMaxSize = MaxRecordValueSize
)

func normalizeMailboxPolicy(policy MailboxPolicy) MailboxPolicy {
	if policy.MaxMsgBytes == 0 {
		policy.MaxMsgBytes = DefaultMailboxMaxMsgBytes
	}
	if policy.MaxMessages == 0 {
		policy.MaxMessages = DefaultMailboxMaxMessages
	}
	if policy.MaxMsgBytesPerSender == 0 {
		policy.MaxMsgBytesPerSender = DefaultMailboxMaxMsgBytesPerSender
	}
	if policy.MaxMessagesPerSender == 0 {
		policy.MaxMessagesPerSender = DefaultMailboxMaxMessagesPerSender
	}
	if policy.MaxMsgSize == 0 {
		policy.MaxMsgSize = DefaultMailboxMaxMsgSize
	}
	if policy.MaxMsgTTL == 0 {
		policy.MaxMsgTTL = DefaultMailboxMaxMsgTTL
	}
	if policy.MaxShareBytes == 0 {
		policy.MaxShareBytes = DefaultMailboxMaxShareBytes
	}
	if policy.MaxShares == 0 {
		policy.MaxShares = DefaultMailboxMaxShares
	}
	if policy.MaxShareSize == 0 {
		policy.MaxShareSize = DefaultMailboxMaxShareSize
	}
	if policy.MaxShareTTL == 0 {
		policy.MaxShareTTL = DefaultMailboxMaxShareTTL
	}
	return policy
}

func normalizeBlobPolicy(policy BlobPolicy) BlobPolicy {
	if policy.MaxValueSize == 0 || policy.MaxValueSize > wire.MaxDKVSBlobValueSize {
		policy.MaxValueSize = DefaultBlobMaxValueSize
	}
	if policy.MaxFreeLocalKeysPerSigner == 0 {
		policy.MaxFreeLocalKeysPerSigner = DefaultBlobMaxFreeLocalKeysPerSigner
	}
	return policy
}

func normalizeTmpPolicy(policy TmpPolicy) TmpPolicy {
	if policy.MaxTTL == 0 {
		policy.MaxTTL = DefaultTmpMaxTTL
	}
	if policy.MaxSize == 0 {
		policy.MaxSize = DefaultTmpMaxSize
	}
	return policy
}
