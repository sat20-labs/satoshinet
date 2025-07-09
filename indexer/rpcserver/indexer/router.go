package indexer

import (
	"github.com/gin-gonic/gin"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

type Service struct {
	handle *Handle
}

func NewService(indexer shareIndexer.Indexer) *Service {
	return &Service{
		handle: NewHandle(indexer),
	}
}

func (s *Service) InitRouter(r *gin.Engine, proxy string) {

	r.GET(proxy+"/health", s.handle.getHealth)

	//获取地址上大于指定value的utxo;如果value=0,获得所有可用的utxo
	r.GET(proxy+"/utxo/address/:address/:value", s.handle.getPlainUtxos)
	//获取地址上获得所有utxo
	r.GET(proxy+"/allutxos/address/:address", s.handle.getAllUtxos)

	// root group
	// 当前网络高度
	r.GET(proxy+"/bestheight", s.handle.getBestHeight)
	r.GET(proxy+"/height/:height", s.handle.getBlockInfo)

	r.GET(proxy+"/v3/tick/all/:protocol", s.handle.getTickerList)
	r.GET(proxy+"/v3/tick/info/:ticker", s.handle.getTickerInfo)
	r.GET(proxy+"/v3/tick/holders/:ticker", s.handle.getHolderListV3)

	// address
	// 获取某个地址上所有资产和数量的列表
	r.POST(proxy+"/v3/utxos/existing", s.handle.getExistingUtxos)
	r.GET(proxy+"/v3/ascend/:utxo", s.handle.getAscendData)
	r.GET(proxy+"/v3/descend/:utxo", s.handle.getDescendData)
	r.GET(proxy+"/v3/corenode/all", s.handle.getAllCoreNode)
	r.GET(proxy+"/v3/corenode/check/:pubkey", s.handle.checkCoreNode)
	r.GET(proxy+"/v3/corenode/info/:pubkey", s.handle.getCoreNodeInfo)

	r.GET(proxy+"/v3/address/summary/:address", s.handle.getAssetSummaryV3)
	// 获取某个地址上某个资产的utxo数据列表(utxo包含其他资产), ticker格式：wire.AssetName.String()
	r.GET(proxy+"/v3/address/asset/:address/:ticker", s.handle.getUtxosWithTickerV3)
	// 获取utxo的资产信息
	r.GET(proxy+"/v3/utxo/info/:utxo", s.handle.getUtxoInfoV3)
	r.POST(proxy+"/v3/utxos/info", s.handle.getUtxoInfoListV3)

}
