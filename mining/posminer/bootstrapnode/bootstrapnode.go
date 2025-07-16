package bootstrapnode

import (
	"encoding/hex"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/share/indexer"
)

func IsBootStrapNode(pubKey []byte) bool {
	return hex.EncodeToString(pubKey) == common.GetBootstrapPubKey()
}

// 只包含corenode和bootstrap
func IsCoreNode(pubKey []byte) bool {
	if IsBootStrapNode(pubKey) {
		return true
	}

	if hex.EncodeToString(pubKey) == common.GetCoreNodePubKey() {
		return true
	}

	// 从索引器查询结果：该节点已经与引导节点建立了通道，并且将资产质押到通道中（通过HasCoreNodeEligibility判断）
	return indexer.ShareIndexer.IsCoreNode(hex.EncodeToString(pubKey))
}

// 包含所有有资质挖矿的节点
func IsMinerNode(pubKey []byte) bool {
	// 从索引器查询结果：该节点已经与引导节点建立了通道，并且将资产质押到通道中（通过HasCoreNodeEligibility判断）
	return indexer.ShareIndexer.IsMinerNode(hex.EncodeToString(pubKey))
}

func CheckValidator(pubKey string) bool {
	pk, err := hex.DecodeString(pubKey)
	if err != nil {
		return false
	}
	return IsMinerNode(pk)
}
