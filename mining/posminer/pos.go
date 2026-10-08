package posminer

import (
	"bytes"
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/btcutil"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
)

func (vm *ValidatorManager) generatePOSBlock() error {
	vm.posCandidateMutex.Lock()
	defer vm.posCandidateMutex.Unlock()
	if !vm.cfg.IsCurrent() || !vm.cfg.Chain.POSReady() {
		return fmt.Errorf("POS node is not ready")
	}
	best := vm.cfg.Chain.BestSnapshot()
	seq, myself, err := vm.currentMiningState()
	if err != nil {
		return err
	}
	if vm.posCandidate == nil || vm.posCandidate.Header.PrevBlock != best.Hash {
		vm.posCandidate = nil
		candidate, err := vm.cfg.PosMiner.OnTimeGenerateBlock()
		if err != nil {
			return err
		}
		vm.posCandidate = candidate
	}
	block := vm.posCandidate
	bootstrap, err := seq.POSBootstrap(int(best.Height + 1))
	if err != nil {
		return err
	}
	if vm.localValidatorId == bootstrap {
		_, err := vm.cfg.Chain.ApprovePOSBlock(block, vm.localValidatorId, vm.Sign)
		return err
	}
	if myself.Father == nil {
		return fmt.Errorf("no parent for scheduled producer")
	}
	father := vm.cfg.PosMiner.GetPeerByValidatorId(myself.Father.PubKey)
	if father == nil || !father.Connected() {
		return fmt.Errorf("scheduled parent is disconnected")
	}
	var buf bytes.Buffer
	if err := block.Serialize(&buf); err != nil {
		return err
	}
	sig, err := vm.Sign(buf.Bytes())
	if err != nil {
		return err
	}
	if err := father.SendMineBlockAndWait(4*time.Second, wire.CmdBlock, buf.Bytes(), sig); err != nil {
		return err
	}
	// ACK carries no approval signature. Ordinary inventory/download supplies
	// the canonical block and tracks requests; direct getdata here would race
	// that download and trigger the unrequested-block disconnect path.
	return nil
}

func (vm *ValidatorManager) onPOSBlockGenerated(peer *peerpkg.Peer, msg *wire.MsgMineBlock,
	candidate *wire.MsgBlock, height int32) {
	err := vm.handlePOSCandidate(peer, msg, candidate, height)
	var code wire.RejectCode
	reason := ""
	if err != nil {
		code = wire.RejectInvalid
		reason = err.Error()
	}
	peer.QueueMessage(wire.NewMsgMineAckWithCode(msg.Nonce, code, reason), nil)
	if err == nil {
		hash := candidate.BlockHash()
		if vm.cfg.Chain.CanServeBlock(&hash) {
			inv := wire.NewMsgInv()
			_ = inv.AddInvVect(wire.NewInvVect(wire.InvTypeBlock, &hash))
			peer.QueueMessage(inv, nil)
		}
	}
}

func (vm *ValidatorManager) handlePOSCandidate(peer *peerpkg.Peer, msg *wire.MsgMineBlock,
	candidate *wire.MsgBlock, height int32) error {
	if msg.SubCmd != wire.CmdBlock {
		return fmt.Errorf("payload is not a block")
	}
	if !vm.cfg.IsCurrent() || !vm.cfg.Chain.POSReady() {
		return fmt.Errorf("POS node is not ready")
	}
	hash := candidate.BlockHash()
	// ACK loss may cause a retry after the sorter moved. Return success only
	// for this exact canonical block; normal sync supplies its approved bytes.
	if stored, err := vm.cfg.Chain.BlockByHash(&hash); err == nil && stored.Height() == height {
		return nil
	}
	best := vm.cfg.Chain.BestSnapshot()
	seq, myself, err := vm.currentMiningState()
	if err != nil {
		return err
	}
	if height != best.Height+1 || candidate.Header.PrevBlock != best.Hash {
		return fmt.Errorf("candidate does not extend current tip")
	}
	producer, bootstrap, _, err := seq.POSMiningInfo(candidate.Transactions[0], int(height))
	if err != nil {
		return err
	}
	if !vm.VerifyBlockSig(producer, msg.Payload, msg.Sig) {
		return fmt.Errorf("invalid producer payload signature")
	}
	// A substitute must wait for the existing timeout; payout no longer
	// identifies which of Miner/Core/Bootstrap actually signed the candidate.
	if producer != seq.GetCurrentMiningInfo().PubKey &&
		time.Now().Unix()-best.RecvTime < MinerInterval+PreWarningInterval {
		return fmt.Errorf("substitute is not yet eligible online")
	}
	if vm.localValidatorId == bootstrap {
		_, err := vm.cfg.Chain.ApprovePOSBlock(candidate, bootstrap, vm.Sign)
		return err
	}
	slot := seq.GetCurrentMiningInfo()
	if slot.Father == nil || slot.Father.Father == nil || slot.Father.PubKey != vm.localValidatorId || myself.Father == nil || myself.Father.PubKey != bootstrap {
		return fmt.Errorf("local node is not the scheduled relay")
	}
	// Precheck is not acceptance and cannot emit a successful local ACK.
	block := btcutil.NewBlock(candidate)
	if err := vm.cfg.Chain.CheckConnectBlockTemplate(block); err != nil {
		return err
	}
	father := vm.cfg.PosMiner.GetPeerByValidatorId(bootstrap)
	if father == nil || !father.Connected() {
		return fmt.Errorf("scheduled Bootstrap is disconnected")
	}
	return father.SendMineBlockAndWait(4*time.Second, wire.CmdBlock, msg.Payload, msg.Sig)
}
