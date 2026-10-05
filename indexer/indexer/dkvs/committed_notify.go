package dkvs

// Committed events reuse the ordinary notification format. P2P messages never
// carry endpoint-local generation metadata.
func (i *Indexer) emitCommittedEvents(events []batchCASEvent) {
	for _, event := range events {
		i.emit(event.eventType, event.record, event.relay)
	}
}
