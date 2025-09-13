package posminer

import (
	"bytes"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer/validatechaindb"
	"github.com/sat20-labs/satoshinet/mining/posminer/utils"
)

const (
	Version_ValidateChain = 1
)

type ValidateChain struct {
	vcStore      *validatechaindb.ValidateChainStore
	currentState ValidateChainState
}


type ValidateChainState struct {
	LatestHeight     int64
	LatestHash       chainhash.Hash
	LatestEpochIndex int64
}

func NewValidateChain(vcStore *validatechaindb.ValidateChainStore) *ValidateChain {
	if vcStore == nil {
		return nil
	}

	chain := &ValidateChain{
		vcStore: vcStore,
	}

	stateData, err := vcStore.GetState()
	if err == nil {
		chain.currentState.Decode(stateData)
	}

	return chain
}
func (vc *ValidateChain) Start() error {
	err := vc.SyncValidateChain()
	if err != nil {
		return err
	}

	return nil
}

func (vc *ValidateChain) SyncValidateChain() error {
	return nil
}

// 得到本地当前的最新状态
func (vc *ValidateChain) GetCurrentState() *ValidateChainState {
	return &vc.currentState
}

// 更新当前链的最新状态
func (vc *ValidateChain) UpdateCurrentState(state *ValidateChainState) error {
	vc.currentState.LatestHeight = state.LatestHeight
	vc.currentState.LatestHash = state.LatestHash
	vc.currentState.LatestEpochIndex = state.LatestEpochIndex

	stateData, err := vc.currentState.Encode()
	if err != nil {
		return err
	}
	err = vc.vcStore.SetState(stateData)
	if err != nil {
		return err
	}

	return nil
}


func (vcs *ValidateChainState) Encode() ([]byte, error) {
	// Encode the VC state payload.
	var bw bytes.Buffer
	err := utils.WriteElements(&bw, vcs.LatestHeight, vcs.LatestHash, vcs.LatestEpochIndex)
	if err != nil {
		return nil, err
	}

	payload := bw.Bytes()
	return payload, nil
}

func (vcs *ValidateChainState) Decode(stateData []byte) error {

	br := bytes.NewReader(stateData)
	err := utils.ReadElements(br, &vcs.LatestHeight, &vcs.LatestHash, &vcs.LatestEpochIndex)

	return err
}


// 根据Hash获取块数据
func (vc *ValidateChain) GetVCBlock(hash *chainhash.Hash) (*VCBlock, error) {
	dataBlock, err := vc.vcStore.GetBlockData(hash[:])
	if err != nil {
		return nil, err
	}

	vcBlock := &VCBlock{}
	err = vcBlock.Decode(dataBlock)
	if err != nil {
		return nil, err
	}

	return vcBlock, nil
}

// 写入一个块数据
func (vc *ValidateChain) SaveVCBlock(vcBlock *VCBlock) error {
	hash, err := vcBlock.GetHash()
	if err != nil {
		return err
	}

	blockData, err := vcBlock.Encode()
	if err != nil {
		return err
	}
	err = vc.vcStore.SaveBlock(hash[:], blockData)
	if err != nil {
		return err
	}

	return nil
}

// 根据高度获取BlockHash
func (vc *ValidateChain) GetVCBlockHash(height int64) (*chainhash.Hash, error) {
	hash, err := vc.vcStore.GetVCBlockHash(height)
	if err != nil {
		return nil, err
	}

	return hash, nil
}

// 更新一个高度与blockhash
func (vc *ValidateChain) SaveVCBlockHash(height int64, hash *chainhash.Hash) error {
	err := vc.vcStore.SaveVCBlockHash(height, hash)

	return err
}
