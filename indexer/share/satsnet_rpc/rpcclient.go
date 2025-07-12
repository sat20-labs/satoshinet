package satsnet_rpc

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/wire"
)

type BlockOnConnected func(height int32, header *wire.BlockHeader, txns []*btcutil.Tx)
type BlockOnDisconneted func(height int32, header *wire.BlockHeader)

type BtcdClient struct {
	client         *rpcclient.Client
	OnConnected    BlockOnConnected
	OnDisConnected BlockOnDisconneted
}

var _client BtcdClient


func RegisterOnConnected(cb BlockOnConnected) {
	_client.OnConnected = cb
}

func RpcClientReady() bool {
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
		DisableTLS: !enableTls,
	}
	
	client, err := rpcclient.New(connCfg, &ntfnHandlers)
	if err != nil {
		common.Log.Errorf("rpcclient.New failed. %v", err)
		return -1, err
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
	_client.client = client

	return int(blockCount), nil
}

func ShutdownSatsNetClient() {
	_client.client.Shutdown()
}
