package dkvs

import "github.com/sat20-labs/satoshinet/wire"

const (
	DefaultMailboxMaxMsgBytes          = uint64(1024 * 1024)
	DefaultMailboxMaxMessages          = uint64(1024)
	DefaultMailboxMaxMsgBytesPerSender = uint64(128 * 1024)
	DefaultMailboxMaxMessagesPerSender = uint64(128)
	DefaultMailboxMaxMsgSize           = MaxRecordValueSize
	DefaultMailboxMaxMsgTTL            = uint64(30 * 24 * 60 * 60 * 1000)
	DefaultMailboxMaxShareBytes        = uint64(1024 * 1024)
	DefaultMailboxMaxShares            = uint64(256)
	DefaultMailboxMaxShareSize         = MaxRecordValueSize
	DefaultMailboxMaxShareTTL          = uint64(365 * 24 * 60 * 60 * 1000)

	DefaultBlobMaxValueSize              = wire.MaxDKVSBlobValueSize
	DefaultBlobMaxFreeLocalKeysPerSigner = uint64(1)

	DefaultTmpMaxTTL  = uint64(24 * 60 * 60 * 1000)
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
