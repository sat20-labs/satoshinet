package satsnet_rpc

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/wire"
)

type BlockOnConnected func(height int32, header *wire.BlockHeader, txns []*btcutil.Tx)
type BlockOnDisconneted func(height int32, header *wire.BlockHeader)

type BtcdClient struct {
	mutex          sync.Mutex
	client         *rpcclient.Client
	pending        *rpcclient.Client
	initCancel     chan struct{}
	OnConnected    BlockOnConnected
	OnDisConnected BlockOnDisconneted
}

var _client BtcdClient

func RegisterOnConnected(cb BlockOnConnected) {
	_client.OnConnected = cb
}

func RpcClientReady() bool {
	_client.mutex.Lock()
	defer _client.mutex.Unlock()
	return _client.client != nil
}

func InitSatsNetClient(host string, port int, user, passwd, dataPath string,
	enableTls bool) (int, error) {
	ntfnHandlers := rpcclient.NotificationHandlers{
		OnFilteredBlockConnected: func(height int32, header *wire.BlockHeader, txns []*btcutil.Tx) {
			common.Log.Infof("Block connected: %v (%d) %v",
				header.BlockHash(), height, header.Timestamp)
			if _client.OnConnected != nil {
				_client.OnConnected(height, header, txns)
			}
		},
		OnFilteredBlockDisconnected: func(height int32, header *wire.BlockHeader) {
			common.Log.Infof("Block disconnected: %v (%d) %v",
				header.BlockHash(), height, header.Timestamp)
			if _client.OnDisConnected != nil {
				_client.OnDisConnected(height, header)
			}
		},
	}

	var certs []byte
	if enableTls {
		// Connect to local btcd RPC server using websockets.
		certFile := filepath.Join(dataPath, "rpc.cert")
		common.Log.Infof("cert file: %s", certFile)
		var err error
		certs, err = os.ReadFile(certFile)
		if err != nil {
			common.Log.Errorf("ReadFile %s failed, %v", certFile, err)
			return -1, err
		}
	}

	connCfg := &rpcclient.ConnConfig{
		Host:     host + ":" + strconv.Itoa(port),
		User:     user,
		Endpoint: "ws",
		Pass:     passwd,
		//HTTPPostMode: true,
		Certificates: certs,
		DisableTLS:   !enableTls,
	}

	// Keep registration visible to shutdown too: NotifyBlocks and the initial
	// GetBlockCount can otherwise wait forever after the local RPC disconnects.
	cancel := make(chan struct{})
	_client.mutex.Lock()
	_client.initCancel = cancel
	_client.mutex.Unlock()
	var client *rpcclient.Client
	ready := false
	defer func() {
		if !ready && client != nil {
			client.Shutdown()
		}
		_client.mutex.Lock()
		if _client.initCancel == cancel {
			_client.initCancel = nil
			_client.pending = nil
		}
		_client.mutex.Unlock()
	}()
	client, err := rpcclient.New(connCfg, &ntfnHandlers)
	if err != nil {
		common.Log.Errorf("rpcclient.New failed. %v", err)
		return -1, err
	}
	_client.mutex.Lock()
	select {
	case <-cancel:
		_client.mutex.Unlock()
		return -1, rpcclient.ErrClientShutdown
	default:
		_client.pending = client
		_client.mutex.Unlock()
	}

	// Register for block connect and disconnect notifications.
	if err := client.NotifyBlocks(); err != nil {
		common.Log.Errorf("client.NotifyBlocks failed. %v", err)
		return -1, err
	}
	common.Log.Infof("NotifyBlocks: Registration Complete")

	// Get the current block count.
	blockCount, err := client.GetBlockCount()
	if err != nil {
		common.Log.Errorf("client.GetBlockCount failed. %v", err)
		return -1, err
	}
	common.Log.Infof("Block height: %d", blockCount)

	common.Log.Infof("rpc client connected")
	_client.mutex.Lock()
	defer _client.mutex.Unlock()
	select {
	case <-cancel:
		return -1, rpcclient.ErrClientShutdown
	default:
		_client.client = client
		ready = true
	}

	return int(blockCount), nil
}

func ShutdownSatsNetClient() {
	_client.mutex.Lock()
	client, pending := _client.client, _client.pending
	if _client.initCancel != nil {
		select {
		case <-_client.initCancel:
		default:
			close(_client.initCancel)
		}
	}
	_client.mutex.Unlock()
	// Retain the published pointer. In-flight readers can safely finish with
	// ErrClientShutdown without racing a replacement with nil.
	if client != nil {
		client.Shutdown()
	}
	if pending != nil && pending != client {
		pending.Shutdown()
	}
}
