// Copyright (c) 2013-2018 The btcsuite developers
// Copyright (c) 2016-2018 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package validatorpeer

import (
	"container/list"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatorcommand"
	"github.com/sat20-labs/satoshinet/wire"
)

// ConnState represents the state of the requested connection.
type ConnState uint8

// ConnState can be either pending, established, disconnected or failed.  When
// a new connection is requested, it is attempted and categorized as
// established or failed depending on the connection result.  An established
// connection which was disconnected is categorized as disconnected.
const (
	ConnPending ConnState = iota
	ConnFailing
	ConnCanceled
	ConnEstablished
	ConnDisconnected
)

const (

	// pingInterval is the interval of time to wait in between sending ping
	// messages.
	pingInterval         = 120 * time.Second // 2 * time.Minute
	urgent_ping_interval = 1 * time.Second   //

	peerInActiveInterval = 5 * pingInterval // 5 times pingInterval

	peerReconnectMaxTimes = 3
)

type ConnListener interface {
	// An new validator peer is connected
	OnConnDisconnected(*ConnReq)
}

// ConnReq is the connection request to a network address. The
// connection will be retried on disconnection.
type ConnReq struct {
	// The following variables must only be used atomically.
	id uint64

	LocalAddr  net.Addr
	RemoteAddr net.Addr

	Listener ConnListener

	lastSend     int64 // last sent time
	lastReceived int64 // last time it was used
	connClose    int32 // set to 1, the connection has been closed

	CheckNonce uint64
	IsChecked  bool // if a peer connected , it should be checked the remote peer is valid validator peer

	conn net.Conn

	version uint32
	btcnet  wire.BitcoinNet

	CmdsLock    sync.RWMutex
	pendingCmds *list.List // Output commands
	sended      map[validatorcommand.Message]int64

	sendQueue     chan struct{}
	sendDoneQueue chan validatorcommand.Message
	quitQueue     chan struct{}
}

func (connReq *ConnReq) Start() {
	// Start connReq
	connReq.pendingCmds = list.New()

	connReq.sended = make(map[validatorcommand.Message]int64)
	connReq.sendQueue = make(chan struct{}, 1)                     // nonblocking sync
	connReq.sendDoneQueue = make(chan validatorcommand.Message, 1) // nonblocking sync
	connReq.quitQueue = make(chan struct{}, 1)                     // nonblocking sync

	go connReq.sendQueueHandler()
	return
}

func (connReq *ConnReq) String() string {
	return fmt.Sprintf("ConnReq:%s->%s", connReq.LocalAddr, connReq.RemoteAddr)
}

func (connReq *ConnReq) Close() {
	if atomic.LoadInt32(&connReq.connClose) != 0 {
		return
	}
	atomic.StoreInt32(&connReq.connClose, 1)
	connReq.conn.Close()
	// close the send queue
	// 非阻塞写入
	select {
    case connReq.quitQueue <- struct{}{}:
    default:
    }

	if connReq.Listener != nil {
		connReq.Listener.OnConnDisconnected(connReq)
	}
}

func (connReq *ConnReq) setLastReceived() {
	atomic.StoreInt64(&connReq.lastReceived, time.Now().Unix())
}

func (connReq *ConnReq) GetLastReceived() int64 {
	return atomic.LoadInt64(&connReq.lastReceived)
}

func (connReq *ConnReq) isInactive() bool {
	isClosed := atomic.LoadInt32(&connReq.connClose)

	if isClosed != 0 {
		return true
	}

	lastReceivedTime := atomic.LoadInt64(&connReq.lastReceived)
	now := time.Now().Unix()

	intervalLastReceived := now - lastReceivedTime
	if intervalLastReceived > int64(peerInActiveInterval.Seconds()) {
		return true
	}
	return false
}

func (connReq *ConnReq) logConnInfo(desc string) {
	if desc != "" {
		utils.Log.Tracef("------------------------Conn info: %s------------------------", desc)
	}
	utils.Log.Tracef("Conn id: %d", connReq.id)
	utils.Log.Tracef("Conn LocalAddr: %s", connReq.LocalAddr)
	utils.Log.Tracef("Conn RemoteAddr: %s", connReq.RemoteAddr)

	lastReceivedTime := time.Unix(atomic.LoadInt64(&connReq.lastReceived), 0)
	utils.Log.Tracef("Conn Last received time: %s", lastReceivedTime.String())

	lastSendTime := time.Unix(atomic.LoadInt64(&connReq.lastSend), 0)
	utils.Log.Tracef("Conn Last sent time: %s", lastSendTime.String())

	if connReq.pendingCmds != nil {
		utils.Log.Tracef("Pending Commands Count: %d", connReq.pendingCmds.Len())
	}

	// for e := connReq.pendingCmds.Front(); e != nil; e = e.Next() {
	// 	command := e.Value.(*validatorcommand.Message)
	// 	utils.Log.Tracef("Pending Command: %s", command.Command())
	// }
	if desc != "" {
		utils.Log.Tracef("------------------------Conn info: %s End------------------------", desc)
	}
}

func (connReq *ConnReq) SendCommand(command validatorcommand.Message) error {
	if atomic.LoadInt32(&connReq.connClose) != 0 {
		// The connection was closed
		err := fmt.Errorf("connection %s closed", connReq.RemoteAddr)
		utils.Log.Errorf("**********SendCommand (%s) to (%s) failed: %v", command.Command(), connReq.RemoteAddr, err)
		return err
	}

	connReq.AddCommand(command)

	//utils.Log.Tracef("----------[%s]Send signal to send command", connReq.String())
	// Signal the send queue
	select {
	case connReq.sendQueue <- struct{}{}:
	default:
	}

	//utils.Log.Tracef("----------[%s]Command [%s] has sent", connReq.String(), command.Command())
	return nil
}

func (connReq *ConnReq) AddCommand(command validatorcommand.Message) {
	//utils.Log.Tracef("----------[%s]Add command [%s] to send queue", connReq.String(), command.Command())
	connReq.CmdsLock.Lock()
	connReq.pendingCmds.PushBack(command)
	//utils.Log.Tracef("----------[%s]Command count: %d", connReq.String(), connReq.pendingCmds.Len())
	connReq.CmdsLock.Unlock()

	return
}

func (connReq *ConnReq) PopNextCommand() validatorcommand.Message {
	connReq.CmdsLock.Lock()
	defer connReq.CmdsLock.Unlock()

	item := connReq.pendingCmds.Front()
	if item == nil {
		return nil
	}
	connReq.pendingCmds.Remove(item)
	// if item != nil {
	// 	utils.Log.Tracef("----------[%s]Next command [%s] in send queue", connReq.String(), item.Value.(validatorcommand.Message).Command())
	// } else {
	// 	utils.Log.Tracef("----------[%s]No command in send queue", connReq.String())
	// }
	return item.Value.(validatorcommand.Message)
}

func (connReq *ConnReq) WaitCommandSended(command validatorcommand.Message) bool {

	for i := 0; i < 100; i++ {
		connReq.CmdsLock.RLock()
		_, ok := connReq.sended[command]
		connReq.CmdsLock.RUnlock()
		if ok {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}

	return false
}

func (connReq *ConnReq) RemoveItem(item *list.Element) {
	//	utils.Log.Tracef("----------[%s]Remove command [%s] from send queue", connReq.String(), item.Value.(validatorcommand.Message).Command())
	connReq.CmdsLock.Lock()
	connReq.pendingCmds.Remove(item)
	//	utils.Log.Tracef("----------[%s]Command count: %d", connReq.String(), connReq.pendingCmds.Len())
	connReq.CmdsLock.Unlock()
}

// outHandler handles all outgoing messages for the peer.  It must be run as a
// goroutine.  It uses a buffered channel to serialize output messages while
// allowing the sender to continue running asynchronously.
func (connReq *ConnReq) sendQueueHandler() {
out:
	for {
		select {
		case <-connReq.sendQueue:
			//utils.Log.Tracef("----------[%s]Received signal to send command.", connReq.String())
			command := connReq.PopNextCommand()
			if command == nil {
				// No command in queue, wait for the next signal
				//utils.Log.Tracef("----------[%s]No command to be sent.", connReq.String())
				continue
			}

			//command := item.Value.(validatorcommand.Message)
			//utils.Log.Tracef("----------[%s]Will send command [%s].", connReq.String(), command.Command())

			err := connReq.writeMessage(command)
			if err != nil {
				// TODO: writeMessage error, maybe the connect is disconnect
				utils.Log.Errorf("----------[%s]writeMessage failed, %v", connReq.String(), err)
				//connReq.Close()
				break out
			}

			// At this point, the message was successfully sent, so
			// update the last send time, signal the sender of the
			// message that it has been sent (if requested), and
			// signal the send queue to the deliver the next queued
			// message.
			atomic.StoreInt64(&connReq.lastSend, time.Now().Unix())

			utils.Log.Tracef("----------[%s]command [%s] has sent.", connReq.String(), command.Command())
			connReq.CmdsLock.Lock()
			connReq.sended[command] = time.Now().Unix()
			if len(connReq.sended) > 16 {
				min := int64(math.MaxInt64)
				var cmd validatorcommand.Message
				for k, v := range connReq.sended {
					if v < min {
						min = v
						cmd = k
					}
				}
				if cmd != nil {
					delete(connReq.sended, cmd)
				}
			}
			connReq.CmdsLock.Unlock()

			// connReq.sendDoneQueue <- command 需要有线程读取数据，不然会卡住

		// case item := <-connReq.sendDoneQueue:
		// 	// The command was sent successfully, will removed it from the
		// 	// pending command list, and will signal the send queue
		// 	command := item.Value.(validatorcommand.Message)
		// 	utils.Log.Tracef("----------[%s]command [%s] has done.", connReq.String(), command.Command())
		// 	connReq.RemoveItem(item)

		// 	if connReq.pendingCmds.Len() > 0 {
		// 		// command queue not empty, Signal the send queue
		// 		utils.Log.Tracef("----------[%s]Pending command isnot empty.", connReq.String())
		// 		//connReq.sendQueue <- struct{}{}
		// 	} else {
		// 		utils.Log.Tracef("----------[%s]No any command to be send.", connReq.String())
		// 	}

		case <-connReq.quitQueue:
			break out
		}
	}

	utils.Log.Tracef("----------[%d] sendQueueHandler done for %s", connReq.id, connReq.String())
}

// writeMessage sends a bitcoin message to the peer with logging.
func (connReq *ConnReq) writeMessage(msg validatorcommand.Message) error {
	// Don't do anything if we're disconnecting.
	if atomic.LoadInt32(&connReq.connClose) != 0 {
		// The connection was closed
		err := fmt.Errorf("connection %s closed", connReq.RemoteAddr)
		//utils.Log.Tracef("[%s]**********WriteMessage (%s) to (%s) failed: %v", connReq.String(), msg.Command(), connReq.RemoteAddr, err)
		return err
	}

	// Write the message to the peer.
	_, err := validatorcommand.WriteMessageWithEncodingN(connReq.conn, msg,
		connReq.version, connReq.btcnet)
	if err != nil {
		//utils.Log.Tracef("**********[%s]WriteMessage (%s) to (%s) failed: %v", connReq.String(), msg.Command(), connReq.RemoteAddr, err)
		return err
	}

	//utils.Log.Tracef("**********[%s]WriteMessage (%s) to (%s): %d bytes written", connReq.String(), msg.Command(), connReq.RemoteAddr, n)

	return nil
}
