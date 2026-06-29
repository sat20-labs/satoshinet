package evm

import (
	stdsha256 "crypto/sha256"
	"encoding/binary"
	"maps"
	"sort"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

var _ vm.StateDB = (*MemoryStateDB)(nil)

type MemoryStateDB struct {
	accounts  map[gethcommon.Address]*memoryAccount
	triggers  map[triggerKey]Trigger
	logs      []*types.Log
	refund    uint64
	snapshots []memorySnapshot
}

type memoryAccount struct {
	Balance      uint256.Int
	Nonce        uint64
	Code         []byte
	Storage      map[gethcommon.Hash]gethcommon.Hash
	Transient    map[gethcommon.Hash]gethcommon.Hash
	SelfDestruct bool
	NewContract  bool
	Touched      bool
	DeployerAddr string
	Closed       bool
}

type memorySnapshot struct {
	accounts map[gethcommon.Address]*memoryAccount
	triggers map[triggerKey]Trigger
	logs     []*types.Log
	refund   uint64
}

func (s *MemoryStateDB) StateRoot() [32]byte {
	h := stdsha256.New()
	addresses := make([]gethcommon.Address, 0, len(s.accounts))
	for addr := range s.accounts {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return string(addresses[i].Bytes()) < string(addresses[j].Bytes())
	})

	var tmp [8]byte
	for _, addr := range addresses {
		acct := s.accounts[addr]
		h.Write(addr.Bytes())
		binary.BigEndian.PutUint64(tmp[:], acct.Nonce)
		h.Write(tmp[:])
		writeLengthPrefixed(h, acct.Balance.Bytes())
		h.Write(crypto.Keccak256(acct.Code))
		writeLengthPrefixed(h, []byte(acct.DeployerAddr))
		if acct.Closed {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}

		keys := make([]gethcommon.Hash, 0, len(acct.Storage))
		for key := range acct.Storage {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return string(keys[i].Bytes()) < string(keys[j].Bytes())
		})
		for _, key := range keys {
			h.Write(key.Bytes())
			h.Write(acct.Storage[key].Bytes())
		}
	}

	triggerKeys := sortedTriggerKeys(s.triggers)
	for _, key := range triggerKeys {
		trigger := s.triggers[key]
		contractHash := ContractAddressHash(trigger.Contract)
		writeLengthPrefixed(h, []byte(trigger.Contract.Prefix()))
		h.Write([]byte{trigger.Contract.Version(), trigger.Contract.ContractType()})
		h.Write(contractHash[:])
		writeLengthPrefixed(h, []byte(trigger.ID))
		h.Write([]byte{byte(trigger.Kind)})
		binary.BigEndian.PutUint64(tmp[:], uint64(trigger.Height))
		h.Write(tmp[:])
		gasLimit, err := contractframework.GasUnitsUint64(trigger.GasLimit)
		if err != nil {
			gasLimit = 0
		}
		binary.BigEndian.PutUint64(tmp[:], gasLimit)
		h.Write(tmp[:])
		writeLengthPrefixed(h, trigger.Calldata)
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeLengthPrefixed(w byteWriter, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	w.Write(lenBuf[:])
	w.Write(b)
}

func NewMemoryStateDB() *MemoryStateDB {
	return &MemoryStateDB{
		accounts: make(map[gethcommon.Address]*memoryAccount),
		triggers: make(map[triggerKey]Trigger),
	}
}

func (s *MemoryStateDB) CreateAccount(addr gethcommon.Address) {
	s.ensure(addr)
}

func (s *MemoryStateDB) CreateContract(addr gethcommon.Address) {
	acct := s.ensure(addr)
	acct.NewContract = true
}

func (s *MemoryStateDB) SubBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct := s.ensure(addr)
	prev := acct.Balance
	acct.Balance.Sub(&acct.Balance, amount)
	return prev
}

func (s *MemoryStateDB) AddBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct := s.ensure(addr)
	prev := acct.Balance
	acct.Balance.Add(&acct.Balance, amount)
	return prev
}

func (s *MemoryStateDB) GetBalance(addr gethcommon.Address) *uint256.Int {
	acct := s.ensure(addr)
	return new(uint256.Int).Set(&acct.Balance)
}

func (s *MemoryStateDB) GetNonce(addr gethcommon.Address) uint64 {
	return s.ensure(addr).Nonce
}

func (s *MemoryStateDB) SetNonce(addr gethcommon.Address, nonce uint64, reason tracing.NonceChangeReason) {
	s.ensure(addr).Nonce = nonce
}

func (s *MemoryStateDB) GetCodeHash(addr gethcommon.Address) gethcommon.Hash {
	code := s.ensure(addr).Code
	if len(code) == 0 {
		return gethcommon.Hash{}
	}
	return crypto.Keccak256Hash(code)
}

func (s *MemoryStateDB) GetCode(addr gethcommon.Address) []byte {
	return contractframework.CloneBytes(s.ensure(addr).Code)
}

func (s *MemoryStateDB) SetCode(addr gethcommon.Address, code []byte, reason tracing.CodeChangeReason) []byte {
	acct := s.ensure(addr)
	prev := contractframework.CloneBytes(acct.Code)
	acct.Code = contractframework.CloneBytes(code)
	return prev
}

func (s *MemoryStateDB) GetCodeSize(addr gethcommon.Address) int {
	return len(s.ensure(addr).Code)
}

func (s *MemoryStateDB) AddRefund(v uint64) {
	s.refund += v
}

func (s *MemoryStateDB) SubRefund(v uint64) {
	if v > s.refund {
		s.refund = 0
		return
	}
	s.refund -= v
}

func (s *MemoryStateDB) GetRefund() uint64 {
	return s.refund
}

func (s *MemoryStateDB) GetStateAndCommittedState(addr gethcommon.Address, key gethcommon.Hash) (gethcommon.Hash, gethcommon.Hash) {
	value := s.GetState(addr, key)
	return value, value
}

func (s *MemoryStateDB) GetState(addr gethcommon.Address, key gethcommon.Hash) gethcommon.Hash {
	return s.ensure(addr).Storage[key]
}

func (s *MemoryStateDB) GetStorageRoot(addr gethcommon.Address) gethcommon.Hash {
	return gethcommon.Hash{}
}

func (s *MemoryStateDB) SetState(addr gethcommon.Address, key, value gethcommon.Hash) gethcommon.Hash {
	acct := s.ensure(addr)
	prev := acct.Storage[key]
	if value == (gethcommon.Hash{}) {
		delete(acct.Storage, key)
	} else {
		acct.Storage[key] = value
	}
	return prev
}

func (s *MemoryStateDB) GetTransientState(addr gethcommon.Address, key gethcommon.Hash) gethcommon.Hash {
	return s.ensure(addr).Transient[key]
}

func (s *MemoryStateDB) SetTransientState(addr gethcommon.Address, key, value gethcommon.Hash) {
	acct := s.ensure(addr)
	if value == (gethcommon.Hash{}) {
		delete(acct.Transient, key)
	} else {
		acct.Transient[key] = value
	}
}

func (s *MemoryStateDB) SelfDestruct(addr gethcommon.Address) {
	s.ensure(addr).SelfDestruct = true
}

func (s *MemoryStateDB) HasSelfDestructed(addr gethcommon.Address) bool {
	return s.ensure(addr).SelfDestruct
}

func (s *MemoryStateDB) Exist(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	return ok && !acct.Closed
}

func (s *MemoryStateDB) SetContractDeployer(addr gethcommon.Address, recipient string) {
	acct := s.ensure(addr)
	acct.DeployerAddr = recipient
}

func (s *MemoryStateDB) ContractDeployer(addr gethcommon.Address) (string, bool) {
	acct, ok := s.accounts[addr]
	if !ok {
		return "", false
	}
	return acct.DeployerAddr, true
}

func (s *MemoryStateDB) CloseContract(addr gethcommon.Address) {
	s.ensure(addr).Closed = true
}

func (s *MemoryStateDB) ContractClosed(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	return ok && acct.Closed
}

func (s *MemoryStateDB) Touch(addr gethcommon.Address) {
	s.ensure(addr).Touched = true
}

func (s *MemoryStateDB) IsNewContract(addr gethcommon.Address) bool {
	return s.ensure(addr).NewContract
}

func (s *MemoryStateDB) Empty(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	if !ok {
		return true
	}
	return acct.Nonce == 0 && acct.Balance.IsZero() && len(acct.Code) == 0
}

func (s *MemoryStateDB) AddressInAccessList(addr gethcommon.Address) bool { return true }

func (s *MemoryStateDB) SlotInAccessList(addr gethcommon.Address, slot gethcommon.Hash) (bool, bool) {
	return true, true
}

func (s *MemoryStateDB) AddAddressToAccessList(addr gethcommon.Address) {}

func (s *MemoryStateDB) AddSlotToAccessList(addr gethcommon.Address, slot gethcommon.Hash) {}

func (s *MemoryStateDB) Prepare(rules params.Rules, sender, coinbase gethcommon.Address, dest *gethcommon.Address, precompiles []gethcommon.Address, txAccesses types.AccessList) {
}

func (s *MemoryStateDB) RevertToSnapshot(id int) {
	if id < 0 || id >= len(s.snapshots) {
		return
	}
	snap := s.snapshots[id]
	s.accounts = cloneAccounts(snap.accounts)
	s.triggers = cloneTriggers(snap.triggers)
	s.logs = cloneLogs(snap.logs)
	s.refund = snap.refund
	s.snapshots = s.snapshots[:id]
}

func (s *MemoryStateDB) Snapshot() int {
	id := len(s.snapshots)
	s.snapshots = append(s.snapshots, memorySnapshot{
		accounts: cloneAccounts(s.accounts),
		triggers: cloneTriggers(s.triggers),
		logs:     cloneLogs(s.logs),
		refund:   s.refund,
	})
	return id
}

func (s *MemoryStateDB) AddLog(log *types.Log) {
	cp := *log
	s.logs = append(s.logs, &cp)
}

func (s *MemoryStateDB) EmitLogsForBurnAccounts() {}

func (s *MemoryStateDB) AddPreimage(hash gethcommon.Hash, preimage []byte) {}

func (s *MemoryStateDB) Witness() *stateless.Witness { return nil }

func (s *MemoryStateDB) AccessEvents() *state.AccessEvents { return nil }

func (s *MemoryStateDB) Finalise(deleteEmptyObjects bool) {}

func (s *MemoryStateDB) Logs() []*types.Log {
	return cloneLogs(s.logs)
}

func (s *MemoryStateDB) ensure(addr gethcommon.Address) *memoryAccount {
	acct, ok := s.accounts[addr]
	if ok {
		return acct
	}
	acct = &memoryAccount{
		Storage:   make(map[gethcommon.Hash]gethcommon.Hash),
		Transient: make(map[gethcommon.Hash]gethcommon.Hash),
	}
	s.accounts[addr] = acct
	return acct
}

func cloneAccounts(src map[gethcommon.Address]*memoryAccount) map[gethcommon.Address]*memoryAccount {
	dst := make(map[gethcommon.Address]*memoryAccount, len(src))
	for addr, acct := range src {
		cp := *acct
		cp.Code = contractframework.CloneBytes(acct.Code)
		cp.Storage = maps.Clone(acct.Storage)
		cp.Transient = maps.Clone(acct.Transient)
		dst[addr] = &cp
	}
	return dst
}

func cloneLogs(src []*types.Log) []*types.Log {
	if src == nil {
		return nil
	}
	dst := make([]*types.Log, len(src))
	for i, log := range src {
		cp := *log
		dst[i] = &cp
	}
	return dst
}
