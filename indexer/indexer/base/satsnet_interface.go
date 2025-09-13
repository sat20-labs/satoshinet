package base


/*
 提供一些数据接口，供聪网节点实时查询数据。
 被访问的数据需要加锁
*/

func (b *BaseIndexer) IsCoreNode(pubkey string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	_, ok := b.coreNodeMap[pubkey]
	return ok
}

func (b *BaseIndexer) IsMinerNode(pubkey string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	_, ok := b.coreNodeMap[pubkey]
	if ok {
		return true
	}

	for _, v := range b.coreNodeMap {
		_, ok := v.ChildMiners[pubkey]
		if ok {
			return true
		}
	}

	return false
}

// 挖矿顺序
func (b *BaseIndexer) GetMiningSequence() []string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	
	return nil
}

// 当前要出块的地址
func (b *BaseIndexer) GetCurrentMiningAddr() string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return ""
}

// 上个出块地址，或者说当前链最高区块的挖矿地址
func (b *BaseIndexer) GetPrevMiningAddr() string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.miningAddress
}

// 下个出块地址
func (b *BaseIndexer) GetNextMiningAddr() string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return ""
}

