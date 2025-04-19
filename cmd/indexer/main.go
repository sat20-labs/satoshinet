package main

import (
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer"
	"github.com/sirupsen/logrus"
)

func NewLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.TraceLevel)
	return log
}

func main() {

	NewLogger()

	anchorCfg := &anchortx.AnchorConfig{
		IndexerScheme: "http",
		IndexerHost:   "127.0.0.1",
		IndexerProxy:  "testnet",
		ChainParams:   &chaincfg.TestNetParams,
	}
	if !anchortx.StartAnchorManager(anchorCfg) {
		return
	}
	
	stopChan := make(<-chan struct{})
	mgr, err := indexer.NewIndexerMgr("./data", 
	"192.168.10.103",
	"19527", 
	"q17AIoqBJSEhW7djqjn0nTsZcz4=", "nnlkAZn58bqsyYwVtHIajZ16cj8=", false, true,
		stopChan)
	if err != nil {
		return
	}
	mgr.Start()

	for  {
		select {
		
		case <-stopChan:
			break
		}
	}
	
}