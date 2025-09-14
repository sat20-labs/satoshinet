package base

import (
	"encoding/hex"
	"sort"
	"sync"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

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

/* 
下面的接口，假定索引器的高度就是当前高度，在当前高度下的挖矿顺序和挖矿地址
基本规则：
	1. 按公钥顺序
	2. 分层次：先引导节点（A，B，C...）->核心节点（A1,A2,A3...）->普通节点(A11,A12,A13...)
	3. 替补规则：子节点不在线，由父节点替补挖矿，不能漏掉
	
	A ->  A1 -- A11,A12,A13
		A2 -- A21,A22,A23
		A3 -- A31,A32,A33
	B -> B1 -- B11,B12,B13
		B2 -- B21,B22,B23
		B3 -- B31,B32,B33
	C -> C1 -- C11,C12,C13
		C2 -- C21,C22,C23
		C3 -- C31,C32,C33
	挖矿顺序：A,A1,A11,A12,A13,A2,A21,A22,A23,A3,A31,A32,A33,B,B1,B11,B12,B13,...到最后再轮回A,A1,A11...
*/

type MiningInfo struct {
	PubKey        string
	MiningAddress string
	Father        *MiningInfo
	Children      []*MiningInfo
	Next          *MiningInfo
	Prev          *MiningInfo
}

type MiningSequenceMgr struct {
	chainParam     *chaincfg.Params
	currHeight     int
	currMiningNode *MiningInfo
	sequence       []*MiningInfo
	nodes          map[string]*MiningInfo // 快速索引, pubkey
	addressMap     map[string]*MiningInfo // 快速索引, address
	mutex          sync.RWMutex
}

// 构造函数
func NewMiningSequenceMgr(chainParam *chaincfg.Params) *MiningSequenceMgr {
	return &MiningSequenceMgr{
		chainParam: chainParam,
		nodes:    make(map[string]*MiningInfo),
		addressMap: make(map[string]*MiningInfo),
		sequence: make([]*MiningInfo, 0),
	}
}

// 添加节点
func (b *MiningSequenceMgr) AddNode(pubkey, father string) (*MiningInfo, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if _, ok := b.nodes[pubkey]; ok {
		return b.nodes[pubkey], nil // 已存在
	}

	pubkeyA, err := hex.DecodeString(pubkey)
	if err != nil {
		return nil, err
	}
	var channelAddr string
	if father != "" {
		
		pubkeyB, err := hex.DecodeString(father)
		if err != nil {
			return nil, err
		}
		channelAddr, err = common.GetChannelAddress(pubkeyA, pubkeyB, b.chainParam)
		if err != nil {
			return nil, err
		}
	} else {
		channelAddr, err = common.PubKeyBytesToP2TRAddress(pubkeyA, b.chainParam)
		if err != nil {
			return nil, err
		}
	}

	node := &MiningInfo{
		PubKey:        pubkey,
		MiningAddress: channelAddr,
	}
	b.nodes[pubkey] = node
	b.addressMap[channelAddr] = node

	if father != "" {
		if f, ok := b.nodes[father]; ok {
			node.Father = f
			f.Children = append(f.Children, node)
			// 保持孩子按公钥排序
			sort.Slice(f.Children, func(i, j int) bool {
				return f.Children[i].PubKey < f.Children[j].PubKey
			})
		}
	}

	b.rebuildSequence()
	return node, nil
}

// 删除节点
func (b *MiningSequenceMgr) RemoveNode(pubkey string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	n, ok := b.nodes[pubkey]
	if !ok {
		return
	}
	delete(b.nodes, pubkey)
	delete(b.addressMap, n.MiningAddress)

	if n.Father != nil {
		children := n.Father.Children
		for i, c := range children {
			if c == n {
				n.Father.Children = append(children[:i], children[i+1:]...)
				break
			}
		}
	}
	b.rebuildSequence()
}

// 重建挖矿顺序
func (b *MiningSequenceMgr) rebuildSequence() {
	seq := make([]*MiningInfo, 0)

	// 找到顶层（没有父节点的），按 PubKey 排序
	var roots []*MiningInfo
	for _, n := range b.nodes {
		if n.Father == nil {
			roots = append(roots, n)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].PubKey < roots[j].PubKey
	})

	var dfs func(n *MiningInfo)
	dfs = func(n *MiningInfo) {
		seq = append(seq, n)
		for _, c := range n.Children {
			dfs(c)
		}
	}
	for _, r := range roots {
		dfs(r)
	}

	// 建立 Next/Prev 链表
	for i := 0; i < len(seq); i++ {
		if i > 0 {
			seq[i].Prev = seq[i-1]
		}
		if i < len(seq)-1 {
			seq[i].Next = seq[i+1]
		}
	}
	// 环状
	if len(seq) > 1 {
		seq[0].Prev = seq[len(seq)-1]
		seq[len(seq)-1].Next = seq[0]
	}

	b.sequence = seq
	if b.currMiningNode == nil && len(seq) > 0 {
		b.currMiningNode = seq[0]
	}
}


// 设置当前挖矿地址
func (b *MiningSequenceMgr) SetCurrentMiningAddr(addr string, height int) string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.currMiningNode == nil {
		return ""
	}
	return b.currMiningNode.MiningAddress
}

// 当前挖矿地址
func (b *MiningSequenceMgr) GetCurrentMiningAddr() string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.currMiningNode == nil {
		return ""
	}
	return b.currMiningNode.MiningAddress
}

// 上一个挖矿地址
func (b *MiningSequenceMgr) GetPrevMiningAddr() string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.currMiningNode == nil {
		return ""
	}
	return b.currMiningNode.Prev.MiningAddress
}

// 下一个挖矿地址（需要由调用方确定是否在线）
func (b *MiningSequenceMgr) GetNextMiningAddr() string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.currMiningNode == nil {
		return ""
	}
	return b.currMiningNode.Next.MiningAddress
}
