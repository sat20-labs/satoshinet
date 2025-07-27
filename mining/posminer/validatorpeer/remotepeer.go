// Copyright (c) 2013-2018 The btcsuite developers
// Copyright (c) 2016-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorpeer

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/bootstrapnode"
	"github.com/sat20-labs/satoshinet/mining/posminer/epoch"
	"github.com/sat20-labs/satoshinet/mining/posminer/generator"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorinfo"
	"github.com/sat20-labs/satoshinet/wire"
)

// RemotePeerInterface defines callback function pointers to invoke by local
// peers. Include notify from peer to manager, and get info from manager.

// All notify functions should be start with "On". And all get functions should
// be start with "Get".
type RemotePeerInterface interface {
	// OnPeerDisconnected is invoked when a remote peer connects to the local peer .
	OnPeerDisconnected(net.Addr)

	OnValidatorInfoUpdated(validatorInfo *validatorinfo.ValidatorInfo, changeMask validatorinfo.ValidatorInfoMask)

	OnAllValidatorsResponse(validatorInfo []validatorinfo.ValidatorInfo)

	OnEpochResponse(currentEpoch *epoch.Epoch, nextEpoch *epoch.Epoch)

	OnGeneratorResponse(generatorInfo *generator.Generator)

	// New epoch command is received
	OnNewEpoch(validatorId string, hash *chainhash.Hash)

	// GetLocalValidatorInfo invoke when local validator info.
	GetLocalValidatorInfo() *validatorinfo.ValidatorInfo

	// Notify when del epoch member is confirmed
	OnConfirmedDelEpochMember(delEpochMember *epoch.DelEpochMember)

	// Received a vc state command
	OnVCState(*validatorcommand.MsgVCState, net.Addr)

	// Received a vc list command
	OnVCList(*validatorcommand.MsgVCList, net.Addr)

	// Received a vc block command
	OnVCBlock(*validatorcommand.MsgVCBlock, net.Addr)
}

// Config is the struct to hold configuration options useful to localpeer.
type RemotePeerConfig struct {
	// HostToNetAddress returns the netaddress for the given host. This can be
	// nil in  which case the host will be parsed as an IP address.
	HostToNetAddress HostToNetAddrFunc

	// Proxy indicates a proxy is being used for connections.  The only
	// effect this has is to prevent leaking the tor proxy address, so it
	// only needs to specified if using a tor proxy.
	Proxy string

	// UserAgentName specifies the user agent name to advertise.  It is
	// highly recommended to specify this value.
	UserAgentName string

	// ChainParams identifies which chain parameters the peer is associated
	// with.  It is highly recommended to specify this field, however it can
	// be omitted in which case the test network will be used.
	ChainParams *chaincfg.Params

	// ValidatorVersion specifies the maximum validator version to use and
	// advertise.  This field can be omitted in which case
	// peer.MaxProtocolVersion will be used.
	ValidatorVersion uint32

	// RemotePeerInterface to be used by peer manager.
	RemoteValidatorListener RemotePeerInterface

	// TrickleInterval is the duration of the ticker which trickles down the
	// inventory to a peer.
	TrickleInterval time.Duration

	// AllowSelfConns is only used to allow the tests to bypass the self
	// connection detecting and disconnect logic since they intentionally
	// do so for testing purposes.
	AllowSelfConns bool

	// Dial connects to the address on the named network. It cannot be nil.
	Dial   func(net.Addr) (net.Conn, error)
	Lookup func(string) ([]net.IP, error)

	LocalValidatorId  string // local validator id
	RemoteValidatorId string // remote validator id

}

// NOTE: The overall data flow of a peer is split into 3 goroutines.  Inbound
// messages are read via the inHandler goroutine and generally dispatched to
// their own handler.  For inbound data-related messages such as blocks,
// transactions, and inventory, the data is handled by the corresponding
// message handlers.  The data flow for outbound messages is split into 2
// goroutines, queueHandler and outHandler.  The first, queueHandler, is used
// as a way for external entities to queue messages, by way of the QueueMessage
// function, quickly regardless of whether the peer is currently sending or not.
// It acts as the traffic cop between the external world and the actual
// goroutine which writes to the network socket.

// localpeer provides a basic concurrent safe bitcoin peer for handling bitcoin
// communications via the peer-to-peer protocol.  It provides full duplex
// reading and writing, automatic handling of the initial handshake process,
// querying of usage statistics and other information about the remote peer such
// as its address, user agent, and protocol version, output message queuing,
// inventory trickling, and the ability to dynamically register and unregister
// callbacks for handling bitcoin protocol messages.
//
// Outbound messages are typically queued via QueueMessage or QueueInventory.
// QueueMessage is intended for all messages, including responses to data such
// as blocks and transactions.  QueueInventory, on the other hand, is only
// intended for relaying inventory as it employs a trickling mechanism to batch
// the inventory together.  However, some helper functions for pushing messages
// of specific types that typically require common special handling are
// provided as a convenience.
type RemotePeer struct {
	// The following variables must only be used atomically.
	bytesReceived uint64
	bytesSent     uint64
	lastSend      int64
	connected     int32
	disconnect    int32

	connReq  *ConnReq // map of all connections, key is conn id, value is conn req
	connLock sync.RWMutex

	timeoutTimes int64 // if the peer is disconnected, will reconnect again, reconnectTimes is recoed the times of reconnect
	//conn net.Conn
	//stop int32
	//wg   sync.WaitGroup

	// These fields are set at creation time and never modified, so they are
	// safe to read from concurrently without a mutex.
	//addrsList []net.Addr // All the addresses reported by the peer
	addr    net.Addr // default addr for connected to peer
	cfg     RemotePeerConfig
	inbound bool

	flagsMtx             sync.Mutex // protects the peer flags below
	na                   *wire.NetAddressV2
	id                   int32
	userAgent            string
	services             wire.ServiceFlag
	versionKnown         bool
	advertisedProtoVer   uint32 // protocol version advertised by remote
	validatorVersion     uint32 // negotiated validator version
	sendHeadersPreferred bool   // peer sent a sendheaders message
	verAckReceived       bool
	witnessEnabled       bool
	sendAddrV2           bool

	// These fields keep track of statistics for the peer and are protected
	// by the statsMtx mutex.
	statsMtx           sync.RWMutex
	timeOffset         int64
	timeConnected      time.Time
	startingHeight     int32
	lastBlock          int32
	lastAnnouncedBlock *chainhash.Hash
	lastPingNonce      uint64    // Set to nonce if we have a pending ping.
	lastPingTime       time.Time // Time we sent last ping.
	lastPingMicros     int64     // Time for last ping to return.

	//LocalValidatorId uint64 // local validator id

	pingHandleStarted bool
	pingQuit          chan struct{}
}

// String returns the peer's address and directionality as a human-readable
// string.
//
// This function is safe for concurrent access.
func (p *RemotePeer) String() string {
	return fmt.Sprintf("RemotePeer : %s", p.addr)
}

// ID returns the peer id.
//
// This function is safe for concurrent access.
func (p *RemotePeer) ID() int32 {
	p.flagsMtx.Lock()
	id := p.id
	p.flagsMtx.Unlock()

	return id
}

// NA returns the peer network address.
//
// This function is safe for concurrent access.
func (p *RemotePeer) NA() *wire.NetAddressV2 {
	p.flagsMtx.Lock()
	na := p.na
	p.flagsMtx.Unlock()

	return na
}

// Addr returns the peer address.
//
// This function is safe for concurrent access.
func (p *RemotePeer) Addr() string {
	// The address doesn't change after initialization, therefore it is not
	// protected by a mutex.
	return p.addr.String()
}

// Addr returns the peer address.
//
// This function is safe for concurrent access.
func (p *RemotePeer) GetPeerAddr() net.Addr {
	// The address doesn't change after initialization, therefore it is not
	// protected by a mutex.
	return p.addr
}

// UserAgent returns the user agent of the remote peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) UserAgent() string {
	p.flagsMtx.Lock()
	userAgent := p.userAgent
	p.flagsMtx.Unlock()

	return userAgent
}

// LastPingNonce returns the last ping nonce of the remote peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LastPingNonce() uint64 {
	p.statsMtx.RLock()
	lastPingNonce := p.lastPingNonce
	p.statsMtx.RUnlock()

	return lastPingNonce
}

// LastPingTime returns the last ping time of the remote peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LastPingTime() time.Time {
	p.statsMtx.RLock()
	lastPingTime := p.lastPingTime
	p.statsMtx.RUnlock()

	return lastPingTime
}

// LastPingMicros returns the last ping micros of the remote peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LastPingMicros() int64 {
	p.statsMtx.RLock()
	lastPingMicros := p.lastPingMicros
	p.statsMtx.RUnlock()

	return lastPingMicros
}

// ProtocolVersion returns the negotiated peer protocol version.
//
// This function is safe for concurrent access.
func (p *RemotePeer) ValidatorVersion() uint32 {
	p.flagsMtx.Lock()
	validatorVersion := p.validatorVersion
	p.flagsMtx.Unlock()

	return validatorVersion
}

// LastSend returns the last send time of the peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LastSend() time.Time {
	return time.Unix(atomic.LoadInt64(&p.lastSend), 0)
}

// LastRecv returns the last recv time of the peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LastRecv() time.Time {
	//return time.Unix(atomic.LoadInt64(&p.lastRecv), 0)
	lastReceived := 0
	p.connLock.RLock()
	defer p.connLock.RUnlock()
	if p.connReq != nil {
		lastReceived = int(p.connReq.lastReceived)
	}
	return time.Unix(int64(lastReceived), 0)
}

// LocalAddr returns the local address of the connection.
//
// This function is safe for concurrent access.
func (p *RemotePeer) LocalAddr() net.Addr {
	var localAddr net.Addr
	if atomic.LoadInt32(&p.connected) != 0 {
		localAddr = p.connReq.LocalAddr
	}
	return localAddr
}

func (p *RemotePeer) RemoteAddr() net.Addr {
	var remoteAddr net.Addr
	if atomic.LoadInt32(&p.connected) != 0 {
		remoteAddr = p.connReq.RemoteAddr
	}
	return remoteAddr
}

// BytesSent returns the total number of bytes sent by the peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) BytesSent() uint64 {
	return atomic.LoadUint64(&p.bytesSent)
}

// BytesReceived returns the total number of bytes received by the peer.
//
// This function is safe for concurrent access.
func (p *RemotePeer) BytesReceived() uint64 {
	return atomic.LoadUint64(&p.bytesReceived)
}

func (p *RemotePeer) LogConnStats() {
	p.connLock.RLock()
	defer p.connLock.RUnlock()
	if p.connReq == nil {
		utils.Log.Tracef("The validator is disconnected")
		return
	}
	p.connReq.logConnInfo("")
}

// OnConnDisconnected to be called when a connection is disconnected.
//
// This function is safe for concurrent access.
func (p *RemotePeer) OnConnDisconnected(connReq *ConnReq) {
	utils.Log.Tracef("----------[RemotePeer]OnConnDisconnected conn[%d]: %s", connReq.id, connReq.RemoteAddr)

	// The connection is disconnected, set current connection to nil
	atomic.StoreInt32(&p.connected, 0)
	p.connLock.Lock()
	p.connReq = nil
	p.connLock.Unlock()

	// The connection is disconnected, will try to connect again
	if p.pingHandleStarted {
		err := p.Connect()
		if err != nil {
			utils.Log.Tracef("----------[RemotePeer]Reconnect to validator peer failed: %v", err)
			p.Disconnect()
			p.cfg.RemoteValidatorListener.OnPeerDisconnected(p.addr)
		}
	} else {
		p.cfg.RemoteValidatorListener.OnPeerDisconnected(p.addr)
	}
	utils.Log.Tracef("----------[RemotePeer]OnConnDisconnected End")
}

// TimeConnected returns the time at which the peer connected.
//
// This function is safe for concurrent access.
func (p *RemotePeer) TimeConnected() time.Time {
	p.statsMtx.RLock()
	timeConnected := p.timeConnected
	p.statsMtx.RUnlock()

	return timeConnected
}

func (p *RemotePeer) RequestValidatorId(validatorInfo *validatorinfo.ValidatorInfo) {
	p.SendCommand(validatorcommand.NewMsgGetInfo(validatorInfo))
}

func (p *RemotePeer) RequestGetValidators() error {
	p.SendCommand(validatorcommand.NewMsgGetValidators(p.cfg.LocalValidatorId))

	return nil
}

func (p *RemotePeer) RequestGetEpoch() error {

	p.SendCommand(validatorcommand.NewMsgGetEpoch(p.cfg.LocalValidatorId))

	return nil
}

func (p *RemotePeer) RequestGetGenerator() error {

	p.SendCommand(validatorcommand.NewMsgGetGenerator(p.cfg.LocalValidatorId))

	return nil
}

func (p *RemotePeer) SendCommand(command validatorcommand.Message) error {
	p.connLock.RLock()
	connReq := p.connReq
	p.connLock.RUnlock()
	if connReq == nil || connReq.isInactive() {
		utils.Log.Warningf("----------[RemotePeer]The peer is inactive, try to connect to validator: %s", p.String())
		err := p.Connect()
		if err != nil {
			utils.Log.Errorf("----------[RemotePeer]Connect to validator peer failed: %v", err)
			err = errors.New("validator peer is inactive")
			return err
		}

		// reconnect success, get the new connReq
		p.connLock.RLock()
		connReq = p.connReq
		p.connLock.RUnlock()
	}

	connReq.SendCommand(command)
	return nil
}

func (p *RemotePeer) WaitCommandSended(cmd validatorcommand.Message) bool {
	p.connLock.RLock()
	connReq := p.connReq
	p.connLock.RUnlock()
	if connReq == nil || connReq.isInactive() {
		return false
	}
	return connReq.WaitCommandSended(cmd)
}

// newPeerBase returns a new base bitcoin peer based on the inbound flag.  This
// is used by the NewInboundPeer and NewOutboundPeer functions to perform base
// setup needed by both types of peers.
func newRemotePeerBase(origCfg *RemotePeerConfig, inbound bool) *RemotePeer {
	// Default to the max supported protocol version if not specified by the
	// caller.
	cfg := *origCfg // Copy to avoid mutating caller.
	if cfg.ValidatorVersion == 0 {
		cfg.ValidatorVersion = MaxValidatorVersion
	}

	// Set the chain parameters to testnet if the caller did not specify any.
	if cfg.ChainParams == nil {
		cfg.ChainParams = &chaincfg.TestNetParams
	}

	// Set the trickle interval if a non-positive value is specified.
	if cfg.TrickleInterval <= 0 {
		cfg.TrickleInterval = DefaultTrickleInterval
	}

	p := RemotePeer{
		inbound:          inbound,
		cfg:              cfg, // Copy so caller can't mutate.
		validatorVersion: cfg.ValidatorVersion,
		//LocalValidatorId: cfg.LocalValidatorId,
		// connReqIndex: 1,
		// connMap:      make(map[uint64]*ConnReq),
	}
	return &p
}

// NewLocalpeer returns a new local validator peer. If the Config argument
// does not set HostToNetAddress, connecting to anything other than an ipv4 or
// ipv6 address will fail and may cause a nil-pointer-dereference. This
// includes hostnames and onion services.
func NewRemotePeer(cfg *RemotePeerConfig, addr net.Addr) (*RemotePeer, error) {
	p := newRemotePeerBase(cfg, false)

	// p.addrsList = make([]net.Addr, 0, len(addrs))
	// p.addrsList = append(p.addrsList, addrs...)

	utils.Log.Tracef("NewRemotepeer (%s) with local validator ID: %s", addr.String(), p.cfg.LocalValidatorId)

	p.addr = addr

	host, portStr, err := net.SplitHostPort(p.addr.String())
	if err != nil {
		return nil, err
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, err
	}

	if cfg.HostToNetAddress != nil {
		na, err := cfg.HostToNetAddress(host, uint16(port), 0)
		if err != nil {
			return nil, err
		}
		p.na = na
	} else {
		// If host is an onion hidden service or a hostname, it is
		// likely that a nil-pointer-dereference will occur. The caller
		// should set HostToNetAddress if connecting to these.
		p.na = wire.NetAddressV2FromBytes(
			time.Now(), 0, net.ParseIP(host), uint16(port),
		)
	}

	return p, nil
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func (p *RemotePeer) Connected() bool {
	if atomic.LoadInt32(&p.connected) == 0 {
		return false
	}

	p.connLock.RLock()
	defer p.connLock.RUnlock()

	if p.connReq == nil || p.connReq.isInactive() {
		return false
	}

	return true

}

func (p *RemotePeer) Connect() error {
	// Check if the peer is already connected
	if p.Connected() {
		utils.Log.Tracef("***********The validator peer is already connected: %s", p.addr)
		return nil
	}
	conn, err := p.cfg.Dial(p.addr)
	if err != nil {
		utils.Log.Errorf("***********Unable to connect to %s: %v", p.addr, err)
		return err
	}

	newConnReq := &ConnReq{
		id:           1024, // remote peer id
		LocalAddr:    conn.LocalAddr(),
		RemoteAddr:   conn.RemoteAddr(),
		conn:         conn,
		CmdsLock:     sync.RWMutex{},
		btcnet:       p.cfg.ChainParams.Net,
		version:      p.ValidatorVersion(),
		lastReceived: time.Now().Unix(), // default value, connected time is last received time
		Listener:     p,
	}
	//newConnReq.setLast()
	newConnReq.logConnInfo("newConnReq")

	newConnReq.Start()

	atomic.StoreInt32(&p.connected, 1)

	p.connLock.Lock()
	p.connReq = newConnReq
	p.connLock.Unlock()

	p.timeoutTimes = 0
	go p.listenCommand(newConnReq)

	// new pingHandler to send ping timer
	if !p.pingHandleStarted {
		p.pingQuit = make(chan struct{})
		p.pingHandleStarted = true
		go p.pingHandler()
	}

	return nil
}

func (p *RemotePeer) Disconnect() error {
	if p.pingHandleStarted {
		close(p.pingQuit)
		p.pingHandleStarted = false
	}

	p.connLock.RLock()
	connReq := p.connReq
	p.connLock.RUnlock()

	if connReq == nil || connReq.isInactive() {
		return nil
	}
	connReq.Close()
	return nil
}

// pingHandler periodically pings the peer.  It must be run as a goroutine.
func (p *RemotePeer) pingHandler() {
	currentInterval := pingInterval
	pingTicker := time.NewTicker(currentInterval)
	defer pingTicker.Stop()

out:
	for {
		select {
		case <-pingTicker.C:

			if p.timeoutTimes > peerTimeOutMaxTimes {
				utils.Log.Infof("***********Reconnect times too much, will disconnect the validator peer. %d", p.timeoutTimes)
				p.Disconnect()
				p.cfg.RemoteValidatorListener.OnPeerDisconnected(p.addr)
				// The peer is disconnected, will exit ping handler
				break out
			}

			if !p.Connected() {
				utils.Log.Infof("***********Reconnect to validator peer [%s]", p)
				p.Connect()
				continue
			}

			nonce, err := wire.RandomUint64()
			if err != nil {
				utils.Log.Errorf("Not sending ping to %s: %v", p, err)
				continue
			}
			utils.Log.Infof("**********Sending \"ping\" to validator peer [%s] with nonce=%d", p, nonce)
			//p.QueueMessage(validatorcommand.NewMsgPing(nonce), nil)
			p.statsMtx.Lock()
			p.lastPingNonce = nonce
			p.lastPingTime = time.Now()
			p.lastPingMicros = -1 //  wait response with pong
			p.statsMtx.Unlock()

			p.SendCommand(validatorcommand.NewMsgPing(nonce))

			go func() {
				// Wait for check pong，等待时间要小于 urgent_ping_interval 
				nonce := p.LastPingNonce()
				for i := 0; nonce != 0 && i < 8; i++ {
					time.Sleep(time.Second)
					nonce = p.LastPingNonce()
				}

				if nonce != 0 {
					// 超时还没有等到pong消息，启动一个快速ping过程
					p.timeoutTimes++
					utils.Log.Errorf("**********last ping to validator peer [%s] with nonce=%d isnot received.", p, nonce)
					if currentInterval != urgent_ping_interval {
						currentInterval = urgent_ping_interval
						// Last pong isnot received.
						pingTicker.Reset(currentInterval)
					}

				} else {
					utils.Log.Tracef("**********last ping to validator peer [%s] with nonce=%d has received.", p, nonce)
					p.timeoutTimes = 0
					if currentInterval != pingInterval {
						currentInterval = pingInterval
						pingTicker.Reset(currentInterval)
					}
				}
			}()

		case <-p.pingQuit:
			break out
		}
	}
}

func (p *RemotePeer) listenCommand(connReq *ConnReq) {
	for {
		if connReq == nil || connReq.isInactive() {
			// The connection is inactive, will exit listen handler
			break
		}
		utils.Log.Tracef("----------[RemotePeer]Will read command from conn[%d]: %s to %s", connReq.id, connReq.RemoteAddr, connReq.LocalAddr)
		_, command, _, err := validatorcommand.ReadMessage(connReq.conn, p.ValidatorVersion(), p.cfg.ChainParams.Net)
		if err != nil {
			if err == io.EOF {
				utils.Log.Errorf("----------[RemotePeer]conn[%d]: Connection closed by peer。%v", connReq.id, err)
			} else {
				utils.Log.Errorf("----------[RemotePeer]conn[%d]: Read message failed: %v", connReq.id, err)
			}
			// Read message error, disconnect conn.
			connReq.Close()
			return
		}
		utils.Log.Tracef("----------[RemotePeer]Received validator command [%v] from %d", command.Command(), connReq.id)

		connReq.setLastReceived()
		// TODO: handle the command
		go p.handleCommand(connReq, command)
	}
}

func (p *RemotePeer) handleCommand(connReq *ConnReq, command validatorcommand.Message) {
	if connReq == nil {
		// The connection is inactive, will exit listen handler
		return
	}
	utils.Log.Tracef("----------[RemotePeer]handleCommand command [%v] from %s", command.Command(), connReq.RemoteAddr.String())
	switch cmd := command.(type) {
	case *validatorcommand.MsgGetInfo:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgGetInfo command, will response MsgPeerInfo command")
		//cmd.LogCommandInfo()
		// Handle command ping, it will response "PeerInfo" message
		validatorInfo := p.cfg.RemoteValidatorListener.GetLocalValidatorInfo()
		cmdPeerInfo := validatorcommand.NewMsgPeerInfo(validatorInfo)
		connReq.SendCommand(cmdPeerInfo)

		p.HandleRemoteGetInfo(cmd, connReq)

	case *validatorcommand.MsgPeerInfo:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgPeerInfo command")
		//cmd.LogCommandInfo()

		p.HandleRemotePeerInfo(cmd, connReq)

	case *validatorcommand.MsgPing:

		utils.Log.Infof("----------[LocalPeer]Receive ping command %d from %s, will response pong command", cmd.Nonce, connReq.RemoteAddr.String())
		//cmd.LogCommandInfo()
		// Handle command ping, it will response "pong" message
		cmdPong := validatorcommand.NewMsgPong(cmd.Nonce)
		connReq.SendCommand(cmdPong)

	case *validatorcommand.MsgPong:

		utils.Log.Tracef("----------[RemotePeer]Receive pong command")
		//cmd.LogCommandInfo()
		// Handle command ping, it will response "pong" message
		p.handlePongMsg(cmd, connReq)

	case *validatorcommand.MsgGetValidators:
		utils.Log.Tracef("----------[RemotePeer]Receive GetValidators command, it's invalid command for remote peer")
		//cmd.LogCommandInfo()

	case *validatorcommand.MsgValidators:
		utils.Log.Tracef("----------[RemotePeer]Receive Validators command, will notify validatorManager for sync validators")
		//cmd.LogCommandInfo()
		p.HandleValidatorsResponse(cmd, connReq)

	case *validatorcommand.MsgEpoch:
		utils.Log.Tracef("----------[RemotePeer]Receive Epoch command, will notify validatorManager for sync Epoch")
		//cmd.LogCommandInfo()
		p.HandleEpochResponse(cmd, connReq)

	case *validatorcommand.MsgGenerator:
		utils.Log.Tracef("----------[RemotePeer]Receive Generator command, will notify validatorManager for sync Generator")
		//cmd.LogCommandInfo()
		p.HandleGeneratorResponse(cmd, connReq)

	case *validatorcommand.MsgNewEpoch:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgNewEpoch command, will  notify validatorManager for handle MsgNewEpoch command")
		//cmd.LogCommandInfo()
		p.HandleNewEpoch(cmd, connReq)

	case *validatorcommand.MsgConfirmDelEpoch:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgConfirmDelEpoch command, will  notify validatorManager for handle MsgConfirmDelEpoch command")
		//cmd.LogCommandInfo()
		p.HandleConfirmDelEpoch(cmd, connReq)

	case *validatorcommand.MsgVCState:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgVCState command, will  notify validatorManager for handle MsgVCState command")
		//cmd.LogCommandInfo()
		p.HandleVCState(cmd, connReq)

	case *validatorcommand.MsgVCList:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgVCList command, will  notify validatorManager for handle MsgVCList command")
		//cmd.LogCommandInfo()
		p.HandleVCList(cmd, connReq)

	case *validatorcommand.MsgVCBlock:
		utils.Log.Tracef("----------[RemotePeer]Receive MsgVCBlock command, will  notify validatorManager for handle MsgVCBlock command")
		//cmd.LogCommandInfo()
		p.HandleVCBlock(cmd, connReq)

	default:
		utils.Log.Errorf("----------[RemotePeer]Not to handle command [%v] from %d", command.Command(), connReq.id)
	}
}

// handlePongMsg is invoked when a peer receives a pong bitcoin message.  It
// updates the ping statistics as required for recent clients (protocol
// version > BIP0031Version).  There is no effect for older clients or when a
// ping was not previously sent.
func (p *RemotePeer) handlePongMsg(msg *validatorcommand.MsgPong, connReq *ConnReq) {
	// Arguably we could use a buffered channel here sending data
	// in a fifo manner whenever we send a ping, or a list keeping track of
	// the times of each ping. For now we just make a best effort and
	// only record stats if it was for the last ping sent. Any preceding
	// and overlapping pings will be ignored. It is unlikely to occur
	// without large usage of the ping rpc call since we ping infrequently
	// enough that if they overlap we would have timed out the peer.
	p.statsMtx.Lock()
	utils.Log.Infof("----------[RemotePeer]The pong is response from %s, the nonce: %d, last ping nonce: %d", connReq.RemoteAddr.String(), msg.Nonce, p.lastPingNonce)

	if p.lastPingNonce != 0 && msg.Nonce == p.lastPingNonce {
		p.lastPingMicros = time.Since(p.lastPingTime).Nanoseconds()
		p.lastPingMicros /= 1000 // convert to usec.
		p.lastPingNonce = 0
	}
	p.statsMtx.Unlock()
}

func (p *RemotePeer) HandleRemotePeerInfo(peerInfo *validatorcommand.MsgPeerInfo, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator

	if !bootstrapnode.CheckValidator(peerInfo.ValidatorId) {
		utils.Log.Errorf("----------[RemotePeer]The remote peer is not valid, %s", peerInfo.ValidatorId)
		return
	}

	utils.Log.Tracef("----------[RemotePeer]The remote peer info is response, the remote validatorvalidator ID: %s", peerInfo.ValidatorId)
	p.cfg.RemoteValidatorId = peerInfo.ValidatorId

	validatorInfo := validatorinfo.ValidatorInfo{
		ValidatorId: peerInfo.ValidatorId,
		CreateTime:  peerInfo.CreateTime,
	}
	p.cfg.RemoteValidatorListener.OnValidatorInfoUpdated(&validatorInfo, validatorinfo.MaskValidatorId|validatorinfo.MaskCreateTime)
}

func (p *RemotePeer) HandleRemoteGetInfo(getInfo *validatorcommand.MsgGetInfo, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator

	if !bootstrapnode.CheckValidator(getInfo.ValidatorId) {
		utils.Log.Errorf("----------[RemotePeer]The remote peer is not valid, %s", getInfo.ValidatorId)
		return
	}

	utils.Log.Tracef("----------[RemotePeer]The remote peer info is response, the remote validatorvalidator ID: %s", getInfo.ValidatorId)

	p.cfg.RemoteValidatorId = getInfo.ValidatorId

	validatorInfo := validatorinfo.ValidatorInfo{
		ValidatorId: getInfo.ValidatorId,
		CreateTime:  getInfo.CreateTime,
	}
	p.cfg.RemoteValidatorListener.OnValidatorInfoUpdated(&validatorInfo, validatorinfo.MaskValidatorId|validatorinfo.MaskCreateTime)
}

func (p *RemotePeer) HandleValidatorsResponse(validatorsCmd *validatorcommand.MsgValidators, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator
	utils.Log.Tracef("----------[RemotePeer]The remote peer All Validators is response from  validatorvalidator ID: %s", p.cfg.RemoteValidatorId)

	p.cfg.RemoteValidatorListener.OnAllValidatorsResponse(validatorsCmd.Validators)
}

func (p *RemotePeer) HandleEpochResponse(epochCmd *validatorcommand.MsgEpoch, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator
	utils.Log.Tracef("----------[RemotePeer]The epoch is response from  validator ID: %s", p.cfg.RemoteValidatorId)

	p.cfg.RemoteValidatorListener.OnEpochResponse(epochCmd.CurrentEpoch, epochCmd.NextEpoch)
}

func (p *RemotePeer) HandleGeneratorResponse(generatorCmd *validatorcommand.MsgGenerator, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator
	utils.Log.Tracef("----------[RemotePeer]The generator info is response from  validatorvalidator ID: %s", p.cfg.RemoteValidatorId)

	// if generator.IsValid(generatorCmd.GeneratorInfo) == false {
	// 	return
	// }

	p.cfg.RemoteValidatorListener.OnGeneratorResponse(&generatorCmd.GeneratorInfo)
}

func (p *RemotePeer) HandleNewEpoch(newEpochCmd *validatorcommand.MsgNewEpoch, connReq *ConnReq) {
	// 	First check the remote validator is valid, then notify the validator
	utils.Log.Tracef("----------[RemotePeer]The epoch list is response from  validatorvalidator ID: %s", p.cfg.RemoteValidatorId)
	// newEpoch := &epoch.Epoch{
	// 	EpochIndex:      newEpochCmd.EpochIndex,
	// 	CreateHeight:    newEpochCmd.CreateHeight,
	// 	CreateTime:      newEpochCmd.CreateTime,
	// 	ItemList:        make([]*epoch.EpochItem, 0),
	// 	CurGeneratorPos: epoch.Pos_Epoch_NotStarted,
	// }
	// for _, epochItem := range newEpochCmd.ItemList {
	// 	validatorItem := &epoch.EpochItem{
	// 		ValidatorId: epochItem.ValidatorId,
	// 		Host:        epochItem.Host,
	// 		PublicKey:   epochItem.PublicKey,
	// 		Index:       epochItem.Index,
	// 	}
	// 	newEpoch.ItemList = append(newEpoch.ItemList, validatorItem)
	// }

	p.cfg.RemoteValidatorListener.OnNewEpoch(newEpochCmd.ValidatorId, &newEpochCmd.Hash)
}

func (p *RemotePeer) HandleConfirmDelEpoch(confirmDelEpochMemCmd *validatorcommand.MsgConfirmDelEpoch, connReq *ConnReq) {
	p.cfg.RemoteValidatorListener.OnConfirmedDelEpochMember(confirmDelEpochMemCmd.DelEpochMember)
}

func (p *RemotePeer) HandleVCState(vcState *validatorcommand.MsgVCState, connReq *ConnReq) {
	p.cfg.RemoteValidatorListener.OnVCState(vcState, connReq.RemoteAddr)
}

func (p *RemotePeer) HandleVCList(vcList *validatorcommand.MsgVCList, connReq *ConnReq) {
	p.cfg.RemoteValidatorListener.OnVCList(vcList, connReq.RemoteAddr)
}

func (p *RemotePeer) HandleVCBlock(vcBlock *validatorcommand.MsgVCBlock, connReq *ConnReq) {
	p.cfg.RemoteValidatorListener.OnVCBlock(vcBlock, connReq.RemoteAddr)
}
