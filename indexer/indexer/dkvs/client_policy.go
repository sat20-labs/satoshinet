package dkvs

import "github.com/sat20-labs/satoshinet/wire"

// RecordRequiresPathPrecondition reports whether a client write participates
// in network-comparable PathMeta. FREE_LOCAL records are node-local and must
// use PathGeneration zero without a network path precondition.
func RecordRequiresPathPrecondition(record *wire.DKVSRecord) bool {
	return record != nil && !isFreeLocalRecord(record)
}
