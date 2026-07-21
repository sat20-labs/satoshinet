package dkvs

// DefaultBlobPolicy returns the normalized network-independent blob limits
// used when a client has no stricter local policy.
func DefaultBlobPolicy() BlobPolicy {
	return normalizeBlobPolicy(BlobPolicy{})
}
