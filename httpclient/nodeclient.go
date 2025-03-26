package httpclient

import (
	

)



type NodeClient struct {
	*RESTClient
}

func NewNodeClient(scheme, host string, net string) *IndexerClient {
	// net = "mainnet"  -- btc mainnet
	// net = "testnet"  -- btc testnet4, for indexer, it's "testnet"

	
	http := newHTTPClient()

	client := NewRESTClient(scheme, host, net, http)
	return &IndexerClient{client}
}
