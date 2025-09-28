package common

import (
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

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
	JoinHeight    int
	NodeType      int
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


// 重新加载节点
func (b *MiningSequenceMgr) Init(coreNodeMap map[string]*CoreNodeInfo, 
	height int, miningAddr string) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// 先加最顶级的节点
	for bootstrap, v := range coreNodeMap {
		if v.ServerNode == "" {
			node, err := b.addNode(bootstrap, "", v.AscendHeight) // 先加引导节点
			if err != nil {
				return err
			}
			node.NodeType = indexer.NODE_TYPE_BOOTSTRAP
		}
	}

	for core, v := range coreNodeMap {
		if v.ServerNode != "" {
			node, err := b.addNode(core, v.ServerNode, v.AscendHeight) // 先加父节点
			if err != nil {
				return err
			}
			node.NodeType = indexer.NODE_TYPE_CORE

			// 再加子节点
			for child, info := range v.ChildMiners {
				node, err := b.addNode(child, core, info.AscendHeight)
				if err != nil {
					return err
				}
				node.NodeType = indexer.NODE_TYPE_MINER
			}
		}
	}

	b.rebuildSequence()

	if height <= int(b.chainParam.Checkpoints[0].Height) {
		b.currMiningNode = b.sequence[0]
		b.currHeight = height+1
	} else {
		if miningAddr != "" {
			node, ok := b.addressMap[miningAddr]
			if !ok {
				return fmt.Errorf("can't find miner info %s", miningAddr)
			} else {
				b.currMiningNode = node.Next
			}
			b.currHeight = height+1
		} else {
			// 重新建索引数据库
			return fmt.Errorf("mining address is nil, please rebuild indexer database")
		}
	}

	b.DisplaySelf()

	return nil
}


// 添加节点
func (b *MiningSequenceMgr) addNode(pubkey, father string, height int) (*MiningInfo, error) {
	
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
		channelAddr, err = GetChannelAddress(pubkeyA, pubkeyB, b.chainParam)
		if err != nil {
			return nil, err
		}
	} else {
		channelAddr, err = PubKeyBytesToP2TRAddress(pubkeyA, b.chainParam)
		if err != nil {
			return nil, err
		}
	}

	node := &MiningInfo{
		PubKey:        pubkey,
		MiningAddress: channelAddr,
		JoinHeight:    height,
	}
	b.nodes[pubkey] = node
	b.addressMap[channelAddr] = node

	if father != "" {
		if f, ok := b.nodes[father]; ok {
			if f.NodeType < indexer.NODE_TYPE_CORE {
				return nil, fmt.Errorf("father node should be core node or bootstrap node")
			}
			node.Father = f
			f.Children = append(f.Children, node)
			// 保持孩子按公钥排序
			sort.Slice(f.Children, func(i, j int) bool {
				return f.Children[i].PubKey < f.Children[j].PubKey
			})
		} else {
			return nil, fmt.Errorf("can't find father node %s", father)
		}
	}

	return node, nil
}

// 添加节点
func (b *MiningSequenceMgr) AddNode(pubkey, father string, height int) (*MiningInfo, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	node, err := b.addNode(pubkey, father, height)
	if err != nil {
		return nil, err
	}
	if father == "" {
		node.NodeType = indexer.NODE_TYPE_BOOTSTRAP
	} else {
		fatherNode := b.nodes[father]
		switch fatherNode.NodeType {
		case indexer.NODE_TYPE_BOOTSTRAP:
			node.NodeType = indexer.NODE_TYPE_CORE
		case indexer.NODE_TYPE_CORE:
			node.NodeType = indexer.NODE_TYPE_MINER
		default:
			return nil, fmt.Errorf("")
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
				// TODO 以后数量大了后再用二分法
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
}

// 检查当前挖矿地址是否有效
func (b *MiningSequenceMgr) CheckCurrentMiningAddr(addr string) error {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	// 第一个checkpoint，是pos版本升级时，老版本最后一个块
	// 这个高度以下，只需要确认是有效的miner出的块就行，没有顺序
	if b.currHeight <= int(b.chainParam.Checkpoints[0].Height) {
		_, ok := b.addressMap[addr]
		if ok {
			return nil
		}
		if b.chainParam.Name == "testnet" {
			// 测试网络因为普通挖矿节点依赖索引器生成挖矿地址，索引器没有正确配置公钥，导致挖矿地址异常
			if addr == "tb1qgx496h0szk6wtpgpmczu25gmnpnfg2lfp8hc5pedfxr7y50ws3eqt07mqy" {
				return nil
			}
		}
		
		return fmt.Errorf("invalid mining address %s", addr)
	}

	if addr == b.currMiningNode.MiningAddress {
		return nil
	}
	if b.currMiningNode.Father != nil {
		father := b.currMiningNode.Father
		if father.MiningAddress == addr {
			return nil
		}
		if father.Father != nil {
			if father.Father.MiningAddress == addr {
				return nil
			}
		}
	}
	
	return fmt.Errorf("invalid mining address %s", addr)
}

func GetScriptSignData(height int, nonce uint64) []byte {
	return []byte(fmt.Sprintf("%d-%d", height, nonce))
}

func VerifyStandardCoinbaseScript(script, pubkey []byte) (error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing contract path")
	}
	height := tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing invoke result")
	}
	nonce := tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing invoke result")
	}
	sig := tokenizer.Data()
	signature, err := ecdsa.ParseDERSignature(sig)
	if err != nil {
		return err
	}

	publicKey, err := secp256k1.ParsePubKey(pubkey)
	if err != nil {
		return err
	}

	data := GetScriptSignData(int(height), uint64(nonce))
	if !VerifyMessage(publicKey, []byte(data), signature) {
		return fmt.Errorf("VerifyStandardCoinbaseScript VerifyMessage failed")
	}
	return nil
}

// 检查某个高度下的挖矿地址是否有效
func (b *MiningSequenceMgr) CheckMiningAddr(tx *wire.MsgTx, height int, addr string) error {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	// 第一个checkpoint，是pos版本升级时，老版本最后一个块
	// 这个高度以下，只需要确认是有效的miner出的块就行，没有顺序
	if height <= int(b.chainParam.Checkpoints[0].Height) {
		_, ok := b.addressMap[addr]
		if ok {
			return nil
		}
		if b.chainParam.Name == "testnet" {
			// 测试网络因为普通挖矿节点依赖索引器生成挖矿地址，索引器没有正确配置公钥，导致挖矿地址异常
			if addr == "tb1qgx496h0szk6wtpgpmczu25gmnpnfg2lfp8hc5pedfxr7y50ws3eqt07mqy" {
				return nil
			}
		}
		
		return fmt.Errorf("invalid mining address %s", addr)
	}

	// 验证签名
	miningInfo, ok := b.addressMap[addr]
	if !ok {
		return fmt.Errorf("invalid mining address %s", addr)
	}
	pubkey, err := hex.DecodeString(miningInfo.PubKey)
	if err != nil {
		return fmt.Errorf("CheckMiningAddr %v", err)
	}
	err = VerifyStandardCoinbaseScript(tx.TxIn[0].SignatureScript, pubkey)
	if err != nil {
		return fmt.Errorf("CheckMiningAddr %v", err)
	}

	var node *MiningInfo
	if  height < b.currHeight {
		i := b.currHeight
		node = b.currMiningNode
		for i != height {
			i--
			node = node.Prev
			for i <= node.JoinHeight {
				node = node.Prev
			}
		}
	} else {
		i := b.currHeight
		node = b.currMiningNode
		for i != height {
			i++
			node = node.Next
			for i <= node.JoinHeight {
				node = node.Next
			}
		}
	}
	
	if addr == node.MiningAddress {
		return nil
	}
	if node.Father != nil {
		father := node.Father
		if father.MiningAddress == addr {
			return nil
		}
		if father.Father != nil {
			if father.Father.MiningAddress == addr {
				return nil
			}
		}
	}
	
	return fmt.Errorf("invalid mining address %s", addr)
}


// 检查当前挖矿地址是否有效，miner或者其father都是有效节点
func (b *MiningSequenceMgr) CheckCurrentMiningPubKey(pubkey string) error {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	if pubkey == b.currMiningNode.PubKey {
		return nil
	}
	if b.currMiningNode.Father != nil {
		father := b.currMiningNode.Father
		if father.PubKey == pubkey {
			return nil
		}
		if father.Father != nil {
			if father.Father.PubKey == pubkey {
				return nil
			}
		}
	}
	
	return fmt.Errorf("invalid mining pubkey %s", pubkey)
}

// 不能修改返回对象
func (b *MiningSequenceMgr) GetMiningInfoWithAddr(addr string) *MiningInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.addressMap[addr]
}

// 不能修改返回对象
func (b *MiningSequenceMgr) GetMiningInfo(pubkey string) *MiningInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.nodes[pubkey]
}

func (b *MiningSequenceMgr) GetNodeType(pubkey string) int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	node, ok := b.nodes[pubkey]
	if !ok {
		return indexer.NODE_TYPE_NORMAL
	}
	return node.NodeType
}


// 设置当前挖矿地址，每个区块处理完成后调用一次
func (b *MiningSequenceMgr) MoveMiningAddr(height int, addr string) error {
	if height == 0 {
		b.mutex.Lock()
		b.currHeight = 1
		b.mutex.Unlock()
		return nil
	}

	err := b.CheckCurrentMiningAddr(addr)
	if err != nil {
		return err
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	if b.currHeight <= int(b.chainParam.Checkpoints[0].Height) {
		//b.currMiningNode = b.sequence[0]
	} else {
		// addr 有可能是替补地址，所以只移动指针
		// 跳过在这个高度还无效的miner
		b.currMiningNode = b.currMiningNode.Next
		for height <= b.currMiningNode.JoinHeight {
			b.currMiningNode = b.currMiningNode.Next
		}
	}
	b.currHeight++
	
	return nil
}

// 测试接口，不要调用
func (b *MiningSequenceMgr) SetCurrentMiningAddr(addr string) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.currMiningNode = b.addressMap[addr]
}

// 当前挖矿节点
func (b *MiningSequenceMgr) GetCurrentMiningInfo() *MiningInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.currMiningNode
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
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.currMiningNode == nil {
		return ""
	}
	return b.currMiningNode.Next.MiningAddress
}

// 
func (b *MiningSequenceMgr) GetFatherMiningInfo(pubkey string) *MiningInfo {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	node, ok := b.nodes[pubkey]
	if !ok {
		return nil
	}
	return node.Father
}

// 下一次挖矿高度
func (b *MiningSequenceMgr) GetMiningHeightWithAddr(addr string) int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	if b.currMiningNode == nil {
		return -1
	}

	if b.currMiningNode.MiningAddress == addr {
		return b.currHeight
	}

	node := b.currMiningNode.Next
	i := 0
	for node.MiningAddress != addr && node != b.currMiningNode {
		node = node.Next
		i++
	}
	if node.MiningAddress != addr {
		return -1
	}

	return b.currHeight + i
}

func (b *MiningSequenceMgr) DisplaySelf() {
	Log.Debugf("Current sequuncer: %d %s", b.currHeight, b.currMiningNode.PubKey)
	for i, v := range b.sequence {
		Log.Debugf("%d: %d %s %d", i, v.NodeType, v.PubKey, v.JoinHeight)
	}
}
