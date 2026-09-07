package dkvs

// MailboxPolicy returns the normalized local mailbox policy used by this
// endpoint. /mail is AccountBound, so the bound CoreNode is the authority for
// its retention and capacity settings.
func (i *Indexer) MailboxPolicy() MailboxPolicy {
	if i == nil {
		return MailboxPolicy{}
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.mailbox
}
