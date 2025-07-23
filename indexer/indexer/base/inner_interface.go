package base

import "github.com/sat20-labs/satoshinet/wire"

/*
 提供一些内部接口，在跑数据时供内部模块快速访问。
 只能在跑数据的线程中调用。
*/

func (p *BaseIndexer) GetAddressId(address string) uint64 {
	id, _ := p.getAddressId(address)
	return id
}

func (b *BaseIndexer) IsMainnet() bool {
	return b.chaincfgParam.Net == wire.MainNet
}
