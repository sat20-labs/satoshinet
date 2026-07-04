package indexer

import (
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/common"
	dkvs_indexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type Indexer interface {
	Start() error
	Stop()

	IsMainnet() bool
	GetChainParam() *chaincfg.Params
	GetBaseDBVer() string
	GetChainTip() int
	GetSyncHeight() int
	GetInternalSyncHeight() int
	GetBlockInfo(int) (*common.BlockInfo, error)

	// base indexer
	GetAddressById(addressId uint64) string
	GetAddressId(address string) uint64
	GetUtxoById(utxoId uint64) string
	GetUtxoId(utxo string) uint64
	// return: utxoId->value
	GetUTXOsWithAddress(address string) (map[uint64]int64, error)
	// return: utxo, sat ranges

	GetTickerMap(protocol string) map[string]*common.TickerInfo
	GetTickerInfo(tickerName *common.TickerName) *common.TickerInfo
	GetHoldersWithTick(tickerName *common.TickerName) map[string]*indexer.Decimal
	GetBindingSat(ticker *common.TickerName) int
	// Asset
	// return: tick->amount
	GetAssetSummaryInAddressV3(address string) map[common.TickerName]*indexer.Decimal
	// return: tick->UTXOs
	GetAssetUTXOsInAddress(address string) map[common.TickerName][]*common.TxOutput
	// return: utxo->asset amount
	GetAssetUTXOsInAddressWithTickV3(address string, ticker *common.TickerName) (map[uint64]*indexer.AssetsInUtxo, error)
	HasAssetInUtxo(utxo string) bool
	GetTxOutputWithUtxo(utxo string) *common.TxOutput
	GetTxOutputWithUtxoV3(utxo string) *indexer.AssetsInUtxo
	GetAscendData(fundingUtxo string) *common.AscendData
	GetDescendData(nullDataUtxo string) *common.DescendData
	GetChannelLedger(channel string) []*common.ChannelLedgerEntry
	GetChannelStateEvents(channel string) []*common.ChannelStateEvent
	RecordChannelStateEvent(event *common.ChannelStateEvent) error
	GetReferrer(address string) (*common.ReferrerInfo, error)
	GetReferree(name string) map[string]int
	GetAllCoreNode() map[string]*common.CoreNodeInfo
	IsCoreNode(pubkey string) bool
	GetCoreNodeInfo(pubkey string) *common.CoreNodeInfo
	IsMinerNode(pubkey string) bool
	GetMinerInfo(pubkey string) *common.MinerInfo
	GetSeqMgr() *common.MiningSequenceMgr

	GetContractSummaries(start, limit int) ([]contractengine.ContractSummary, int)
	GetContractSummary(address string) (contractengine.ContractSummary, bool)
	GetContractHistory(address string, start, limit int) ([]contractengine.ContractHistoryRecord, int)
	GetEVMSourceMetadata(address string) (contractengine.EVMSourceMetadata, bool)
	PutEVMSourceMetadata(metadata contractengine.EVMSourceMetadata) error

	SetDKVSNotifyCallback(fn dkvs_indexer.NotifyFunc)
	SetDKVSSubscriptionCallback(fn dkvs_indexer.SubscriptionNotifyFunc)
	SetDKVSResolver(resolver dkvs_indexer.DIDResolver)
	SetDKVSFeeVerifier(verifier dkvs_indexer.FeeVerifier)
	SetDKVSSystemVerifier(verifier dkvs_indexer.SystemVerifier)
	PutDKVSRecord(record *wire.DKVSRecord) (bool, error)
	PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error)
	NotifyDKVSNameTransfers(names []string) error
	GetDKVSRecord(key string) (*wire.DKVSRecord, error)
	GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error)
	ListDKVSRecords(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error)
	GetDKVSUsage(prefix string) (*dkvs_indexer.Usage, error)
	SyncDKVSRecords(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error)
	SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs_indexer.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error)
	GetDKVSCheckpoint() (*dkvs_indexer.Checkpoint, error)
	GetDKVSSnapshot() (*dkvs_indexer.Snapshot, error)
	ApplyDKVSSnapshot(snapshot *dkvs_indexer.Snapshot) (int, error)
	PruneExpiredDKVSRecords() (int, error)
	SubscribeDKVS(sub dkvs_indexer.Subscription) ([]*wire.DKVSRecord, int, error)
	UnsubscribeDKVS(sub dkvs_indexer.Subscription) error
	ListDKVSSubscriptions() []dkvs_indexer.Subscription
	IsDKVSSubscribed(key string) bool
}
