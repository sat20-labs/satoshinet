package indexer

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

func dkvsLocalOnly(c *gin.Context) {
	remote := strings.TrimSpace(c.Request.RemoteAddr)
	if remote == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "dkvs local administration only"})
		return
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": -1, "msg": "dkvs local administration only"})
		return
	}
	c.Next()
}

type Service struct {
	handle *Handle
}

func NewService(indexer shareIndexer.Indexer) *Service {
	return &Service{handle: NewHandle(indexer)}
}

func (s *Service) InitRouter(r *gin.Engine, proxy string) {
	r.GET(proxy+"/health", s.handle.getHealth)
	r.GET(proxy+"/utxo/address/:address/:value", s.handle.getPlainUtxos)
	r.GET(proxy+"/allutxos/address/:address", s.handle.getAllUtxos)
	r.GET(proxy+"/bestheight", s.handle.getBestHeight)
	r.GET(proxy+"/height/:height", s.handle.getBlockInfo)

	r.GET(proxy+"/v3/tick/all/:protocol", s.handle.getTickerList)
	r.GET(proxy+"/v3/tick/info/:ticker", s.handle.getTickerInfo)
	r.GET(proxy+"/v3/tick/holders/:ticker", s.handle.getHolderListV3)

	r.POST(proxy+"/v3/utxos/existing", s.handle.getExistingUtxos)
	r.GET(proxy+"/v3/ascend/:utxo", s.handle.getAscendData)
	r.GET(proxy+"/v3/descend/:utxo", s.handle.getDescendData)
	r.GET(proxy+"/v3/channel/ledger/:channel", s.handle.getChannelLedger)
	r.GET(proxy+"/v3/channel/state/:channel", s.handle.getChannelStateEvents)
	r.POST(proxy+"/v3/channel/state", s.handle.recordChannelStateEvent)
	r.GET(proxy+"/v3/referrer/:address", s.handle.getReferrer)
	r.GET(proxy+"/v3/referree/:name", s.handle.getReferree)
	r.GET(proxy+"/v3/corenode/all", s.handle.getAllCoreNode)
	r.GET(proxy+"/v3/corenode/check/:pubkey", s.handle.checkCoreNode)
	r.GET(proxy+"/v3/corenode/info/:pubkey", s.handle.getCoreNodeInfo)
	r.GET(proxy+"/v3/miner/check/:pubkey", s.handle.checkMiner)
	r.GET(proxy+"/v3/miner/info/:pubkey", s.handle.getMinerInfo)

	// DKVS v1 normative path-oriented API.
	r.GET(proxy+"/v3/dkvs/pathmeta", s.handle.getDKVSPathMetaV1)
	r.POST(proxy+"/v3/dkvs/sync/path", s.handle.syncDKVSPath)
	r.POST(proxy+"/v3/dkvs/watch/path", s.handle.watchDKVSPath)
	r.POST(proxy+"/v3/dkvs/records/cas", s.handle.putDKVSRecordCAS)
	r.POST(proxy+"/v3/dkvs/records/batch-cas", s.handle.putDKVSRecordBatchCAS)

	// Read/config and administrative endpoints.
	r.GET(proxy+"/v3/dkvs/records", s.handle.getDKVSRecord)
	r.GET(proxy+"/v3/dkvs/records/prefix", s.handle.listDKVSRecords)
	r.GET(proxy+"/v3/dkvs/usage", s.handle.getDKVSUsage)
	r.GET(proxy+"/v3/dkvs/config", s.handle.getDKVSConfig)
	r.GET(proxy+"/v3/dkvs/checkpoint", s.handle.getDKVSCheckpoint)
	r.GET(proxy+"/v3/dkvs/snapshot", dkvsLocalOnly, s.handle.getDKVSSnapshot)
	r.POST(proxy+"/v3/dkvs/snapshot", dkvsLocalOnly, s.handle.applyDKVSSnapshot)
	r.POST(proxy+"/v3/dkvs/prune", dkvsLocalOnly, s.handle.pruneDKVS)
	r.POST(proxy+"/v3/dkvs/subscriptions", dkvsLocalOnly, s.handle.subscribeDKVS)
	r.DELETE(proxy+"/v3/dkvs/subscriptions", dkvsLocalOnly, s.handle.unsubscribeDKVS)
	r.GET(proxy+"/v3/dkvs/subscriptions", dkvsLocalOnly, s.handle.listDKVSSubscriptions)

	// Development-stage compatibility routes. New SDK code does not use these.
	r.POST(proxy+"/v3/dkvs/records", s.handle.putDKVSRecord)
	r.GET(proxy+"/v3/dkvs/path-meta", s.handle.getDKVSPathMeta)
	r.POST(proxy+"/v3/dkvs/tombstone", s.handle.putDKVSTombstone)
	r.POST(proxy+"/v3/dkvs/sync", s.handle.syncDKVS)
	r.POST(proxy+"/v3/dkvs/watch", s.handle.watchDKVS)
	r.POST(proxy+"/v3/dkvs/sync/directory", s.handle.syncDKVSDirectory)
	r.POST(proxy+"/v3/dkvs/watch/directory", s.handle.watchDKVSDirectory)

	r.GET(proxy+"/v3/address/summary/:address", s.handle.getAssetSummaryV3)
	r.GET(proxy+"/v3/address/utxos/:address", s.handle.getAddressUtxosV3)
	r.GET(proxy+"/v3/address/asset/:address/:ticker", s.handle.getUtxosWithTickerV3)
	r.GET(proxy+"/v3/utxo/info/:utxo", s.handle.getUtxoInfoV3)
	r.POST(proxy+"/v3/utxos/info", s.handle.getUtxoInfoListV3)

	r.GET(proxy+"/v3/contracts", s.handle.getContracts)
	r.GET(proxy+"/v3/contracts/evm/compiler-config", s.handle.getEVMCompilerConfig)
	r.POST(proxy+"/v3/contracts/evm/estimate-deploy", s.handle.estimateEVMDeploy)
	r.POST(proxy+"/v3/contracts/prediction/review-ready", s.handle.reviewPredictionReady)
	r.GET(proxy+"/v3/contracts/:contract", s.handle.getContract)
	r.GET(proxy+"/v3/contracts/:contract/evm/source", s.handle.getEVMSourceMetadata)
	r.POST(proxy+"/v3/contracts/:contract/evm/source", s.handle.putEVMSourceMetadata)
	r.POST(proxy+"/v3/contracts/:contract/evm/estimate-invoke", s.handle.estimateEVMInvoke)
	r.GET(proxy+"/v3/contracts/:contract/state", s.handle.getContractState)
	r.GET(proxy+"/v3/contracts/:contract/history", s.handle.getContractHistory)
	r.GET(proxy+"/v3/contracts/:contract/analytics", s.handle.getContractAnalytics)
	r.GET(proxy+"/v3/contracts/:contract/items/inutxo/:inutxo", s.handle.getContractInvokeItem)
	r.GET(proxy+"/v3/contracts/:contract/users", s.handle.getContractUsers)
	r.GET(proxy+"/v3/contracts/:contract/users/:address", s.handle.getContractUser)
	r.GET(proxy+"/v3/contracts/:contract/users/:address/history", s.handle.getContractUserHistory)
}
