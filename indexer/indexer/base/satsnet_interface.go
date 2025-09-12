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
