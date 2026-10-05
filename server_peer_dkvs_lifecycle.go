package main

// WaitForDisconnect extends the embedded peer.Peer lifecycle with the DKVS
// work owned by this server connection. server.peerDoneHandler already waits
// through this method before reporting the connection as finished. Waiting
// for the wire handlers first prevents new direct dispatch; Close then drains
// delayed DKVS callbacks before the connection can release its Store users.
//
// This intentionally wraps the embedded method, rather than requiring every
// disconnect reason (remote EOF, ban, RPC disconnect, shutdown) to remember a
// separate DKVS cleanup call.
func (sp *serverPeer) WaitForDisconnect() {
	if sp == nil {
		return
	}
	if sp.Peer != nil {
		sp.Peer.WaitForDisconnect()
	}
	wasClosed := sp.dkvsState.Closed()
	sp.dkvsState.Close()
	if !wasClosed && sp.server != nil && !sp.server.dkvsState.Ready() {
		sp.server.RequestDKVSSyncFromMinerPeers()
	}
}
