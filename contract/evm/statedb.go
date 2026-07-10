package evm

import (
	stdsha256 "crypto/sha256"
	"encoding/binary"
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
	accounts   map[gethcommon.Address]*memoryAccount
	triggers   map[triggerKey]Trigger
	logs       []*types.Log
	refund     uint64
	journal    []func()
	revisions  []memoryRevision
	nextRevID  int
	accessList map[gethcommon.Address]map[gethcommon.Hash]struct{}
	committed  map[gethcommon.Address]map[gethcommon.Hash]gethcommon.Hash
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

type memoryRevision struct {
	id           int
	journalIndex int
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
		accounts:   make(map[gethcommon.Address]*memoryAccount),
		triggers:   make(map[triggerKey]Trigger),
		accessList: make(map[gethcommon.Address]map[gethcommon.Hash]struct{}),
		committed:  make(map[gethcommon.Address]map[gethcommon.Hash]gethcommon.Hash),
	}
}

func (s *MemoryStateDB) CreateAccount(addr gethcommon.Address) {
	s.ensure(addr)
}

func (s *MemoryStateDB) CreateContract(addr gethcommon.Address) {
	acct := s.ensure(addr)
	previous := acct.NewContract
	s.appendJournal(func() { acct.NewContract = previous })
	acct.NewContract = true
}

func (s *MemoryStateDB) SubBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct := s.ensure(addr)
	prev := acct.Balance
	s.appendJournal(func() { acct.Balance = prev })
	acct.Balance.Sub(&acct.Balance, amount)
	return prev
}

func (s *MemoryStateDB) AddBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct := s.ensure(addr)
	prev := acct.Balance
	s.appendJournal(func() { acct.Balance = prev })
	acct.Balance.Add(&acct.Balance, amount)
	return prev
}

func (s *MemoryStateDB) GetBalance(addr gethcommon.Address) *uint256.Int {
	acct := s.account(addr)
	if acct == nil {
		return new(uint256.Int)
	}
	return new(uint256.Int).Set(&acct.Balance)
}

func (s *MemoryStateDB) GetNonce(addr gethcommon.Address) uint64 {
	if acct := s.account(addr); acct != nil {
		return acct.Nonce
	}
	return 0
}

func (s *MemoryStateDB) SetNonce(addr gethcommon.Address, nonce uint64, reason tracing.NonceChangeReason) {
	acct := s.ensure(addr)
	prev := acct.Nonce
	s.appendJournal(func() { acct.Nonce = prev })
	acct.Nonce = nonce
}

func (s *MemoryStateDB) GetCodeHash(addr gethcommon.Address) gethcommon.Hash {
	acct := s.account(addr)
	if acct == nil {
		return gethcommon.Hash{}
	}
	code := acct.Code
	if len(code) == 0 {
		return gethcommon.Hash{}
	}
	return crypto.Keccak256Hash(code)
}

func (s *MemoryStateDB) GetCode(addr gethcommon.Address) []byte {
	if acct := s.account(addr); acct != nil {
		return contractframework.CloneBytes(acct.Code)
	}
	return nil
}

func (s *MemoryStateDB) SetCode(addr gethcommon.Address, code []byte, reason tracing.CodeChangeReason) []byte {
	acct := s.ensure(addr)
	prev := contractframework.CloneBytes(acct.Code)
	s.appendJournal(func() { acct.Code = contractframework.CloneBytes(prev) })
	acct.Code = contractframework.CloneBytes(code)
	return prev
}

func (s *MemoryStateDB) GetCodeSize(addr gethcommon.Address) int {
	if acct := s.account(addr); acct != nil {
		return len(acct.Code)
	}
	return 0
}

func (s *MemoryStateDB) AddRefund(v uint64) {
	prev := s.refund
	s.appendJournal(func() { s.refund = prev })
	s.refund += v
}

func (s *MemoryStateDB) SubRefund(v uint64) {
	prev := s.refund
	s.appendJournal(func() { s.refund = prev })
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
	current := s.GetState(addr, key)
	if storage := s.committed[addr]; storage != nil {
		return current, storage[key]
	}
	return current, current
}

func (s *MemoryStateDB) GetState(addr gethcommon.Address, key gethcommon.Hash) gethcommon.Hash {
	if acct := s.account(addr); acct != nil {
		return acct.Storage[key]
	}
	return gethcommon.Hash{}
}

func (s *MemoryStateDB) GetStorageRoot(addr gethcommon.Address) gethcommon.Hash {
	return gethcommon.Hash{}
}

func (s *MemoryStateDB) SetState(addr gethcommon.Address, key, value gethcommon.Hash) gethcommon.Hash {
	acct := s.ensure(addr)
	prev := acct.Storage[key]
	s.appendJournal(func() {
		if prev == (gethcommon.Hash{}) {
			delete(acct.Storage, key)
		} else {
			acct.Storage[key] = prev
		}
	})
	if value == (gethcommon.Hash{}) {
		delete(acct.Storage, key)
	} else {
		acct.Storage[key] = value
	}
	return prev
}

func (s *MemoryStateDB) GetTransientState(addr gethcommon.Address, key gethcommon.Hash) gethcommon.Hash {
	if acct := s.account(addr); acct != nil {
		return acct.Transient[key]
	}
	return gethcommon.Hash{}
}

func (s *MemoryStateDB) SetTransientState(addr gethcommon.Address, key, value gethcommon.Hash) {
	acct := s.ensure(addr)
	prev := acct.Transient[key]
	s.appendJournal(func() {
		if prev == (gethcommon.Hash{}) {
			delete(acct.Transient, key)
		} else {
			acct.Transient[key] = prev
		}
	})
	if value == (gethcommon.Hash{}) {
		delete(acct.Transient, key)
	} else {
		acct.Transient[key] = value
	}
}

func (s *MemoryStateDB) SelfDestruct(addr gethcommon.Address) {
	acct := s.ensure(addr)
	prev := acct.SelfDestruct
	s.appendJournal(func() { acct.SelfDestruct = prev })
	acct.SelfDestruct = true
}

func (s *MemoryStateDB) HasSelfDestructed(addr gethcommon.Address) bool {
	return s.account(addr) != nil && s.account(addr).SelfDestruct
}

func (s *MemoryStateDB) Exist(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	return ok && !acct.Closed
}

func (s *MemoryStateDB) SetContractDeployer(addr gethcommon.Address, recipient string) {
	acct := s.ensure(addr)
	prev := acct.DeployerAddr
	s.appendJournal(func() { acct.DeployerAddr = prev })
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
	acct := s.ensure(addr)
	prev := acct.Closed
	s.appendJournal(func() { acct.Closed = prev })
	acct.Closed = true
}

func (s *MemoryStateDB) ContractClosed(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	return ok && acct.Closed
}

func (s *MemoryStateDB) Touch(addr gethcommon.Address) {
	acct := s.ensure(addr)
	prev := acct.Touched
	s.appendJournal(func() { acct.Touched = prev })
	acct.Touched = true
}

func (s *MemoryStateDB) IsNewContract(addr gethcommon.Address) bool {
	return s.account(addr) != nil && s.account(addr).NewContract
}

func (s *MemoryStateDB) Empty(addr gethcommon.Address) bool {
	acct, ok := s.accounts[addr]
	if !ok {
		return true
	}
	return acct.Nonce == 0 && acct.Balance.IsZero() && len(acct.Code) == 0
}

func (s *MemoryStateDB) AddressInAccessList(addr gethcommon.Address) bool {
	_, ok := s.accessList[addr]
	return ok
}

func (s *MemoryStateDB) SlotInAccessList(addr gethcommon.Address, slot gethcommon.Hash) (bool, bool) {
	slots, addressOK := s.accessList[addr]
	if !addressOK {
		return false, false
	}
	_, slotOK := slots[slot]
	return true, slotOK
}

func (s *MemoryStateDB) AddAddressToAccessList(addr gethcommon.Address) {
	if _, ok := s.accessList[addr]; ok {
		return
	}
	s.appendJournal(func() { delete(s.accessList, addr) })
	s.accessList[addr] = make(map[gethcommon.Hash]struct{})
}

func (s *MemoryStateDB) AddSlotToAccessList(addr gethcommon.Address, slot gethcommon.Hash) {
	s.AddAddressToAccessList(addr)
	if _, ok := s.accessList[addr][slot]; ok {
		return
	}
	s.appendJournal(func() { delete(s.accessList[addr], slot) })
	s.accessList[addr][slot] = struct{}{}
}

func (s *MemoryStateDB) Prepare(rules params.Rules, sender, coinbase gethcommon.Address, dest *gethcommon.Address, precompiles []gethcommon.Address, txAccesses types.AccessList) {
	s.logs = nil
	s.accessList = make(map[gethcommon.Address]map[gethcommon.Hash]struct{})
	s.committed = cloneCommittedStorage(s.accounts)
	s.AddAddressToAccessList(sender)
	s.AddAddressToAccessList(coinbase)
	if dest != nil {
		s.AddAddressToAccessList(*dest)
	}
	for _, addr := range precompiles {
		s.AddAddressToAccessList(addr)
	}
	for _, tuple := range txAccesses {
		s.AddAddressToAccessList(tuple.Address)
		for _, slot := range tuple.StorageKeys {
			s.AddSlotToAccessList(tuple.Address, slot)
		}
	}
}

func (s *MemoryStateDB) RevertToSnapshot(id int) {
	position := s.revisionPosition(id)
	if position < 0 {
		return
	}
	journalIndex := s.revisions[position].journalIndex
	for i := len(s.journal) - 1; i >= journalIndex; i-- {
		s.journal[i]()
	}
	s.journal = s.journal[:journalIndex]
	s.revisions = s.revisions[:position]
}

func (s *MemoryStateDB) Snapshot() int {
	id := s.nextRevID
	s.nextRevID++
	s.revisions = append(s.revisions, memoryRevision{id: id, journalIndex: len(s.journal)})
	return id
}

// DiscardSnapshot commits all changes since id and drops their rollback data.
func (s *MemoryStateDB) DiscardSnapshot(id int) {
	position := s.revisionPosition(id)
	if position < 0 {
		return
	}
	journalIndex := s.revisions[position].journalIndex
	s.journal = s.journal[:journalIndex]
	s.revisions = s.revisions[:position]
}

func (s *MemoryStateDB) AddLog(log *types.Log) {
	cp := *log
	previousLen := len(s.logs)
	s.appendJournal(func() { s.logs = s.logs[:previousLen] })
	s.logs = append(s.logs, &cp)
}

func (s *MemoryStateDB) EmitLogsForBurnAccounts() {}

func (s *MemoryStateDB) AddPreimage(hash gethcommon.Hash, preimage []byte) {}

func (s *MemoryStateDB) Witness() *stateless.Witness { return nil }

func (s *MemoryStateDB) AccessEvents() *state.AccessEvents { return nil }

func (s *MemoryStateDB) Finalise(deleteEmptyObjects bool) {
	for addr, acct := range s.accounts {
		if acct.SelfDestruct || (deleteEmptyObjects && acct.Touched && acct.Nonce == 0 && acct.Balance.IsZero() && len(acct.Code) == 0) {
			previous := acct
			s.appendJournal(func() { s.accounts[addr] = previous })
			delete(s.accounts, addr)
			continue
		}
		if len(acct.Transient) != 0 {
			previous := cloneHashMap(acct.Transient)
			s.appendJournal(func() { acct.Transient = previous })
			acct.Transient = make(map[gethcommon.Hash]gethcommon.Hash)
		}
		acct.NewContract = false
		acct.Touched = false
	}
	s.refund = 0
}

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
	s.appendJournal(func() { delete(s.accounts, addr) })
	s.accounts[addr] = acct
	return acct
}

func (s *MemoryStateDB) account(addr gethcommon.Address) *memoryAccount {
	if s == nil {
		return nil
	}
	return s.accounts[addr]
}

func (s *MemoryStateDB) appendJournal(undo func()) {
	if s != nil && len(s.revisions) != 0 {
		s.journal = append(s.journal, undo)
	}
}

func (s *MemoryStateDB) revisionPosition(id int) int {
	for i := len(s.revisions) - 1; i >= 0; i-- {
		if s.revisions[i].id == id {
			return i
		}
	}
	return -1
}

func cloneAccounts(src map[gethcommon.Address]*memoryAccount) map[gethcommon.Address]*memoryAccount {
	dst := make(map[gethcommon.Address]*memoryAccount, len(src))
	for addr, acct := range src {
		cp := *acct
		cp.Code = contractframework.CloneBytes(acct.Code)
		cp.Storage = cloneHashMap(acct.Storage)
		cp.Transient = cloneHashMap(acct.Transient)
		dst[addr] = &cp
	}
	return dst
}

func cloneHashMap(src map[gethcommon.Hash]gethcommon.Hash) map[gethcommon.Hash]gethcommon.Hash {
	if src == nil {
		return nil
	}
	dst := make(map[gethcommon.Hash]gethcommon.Hash, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneCommittedStorage(accounts map[gethcommon.Address]*memoryAccount) map[gethcommon.Address]map[gethcommon.Hash]gethcommon.Hash {
	out := make(map[gethcommon.Address]map[gethcommon.Hash]gethcommon.Hash, len(accounts))
	for addr, acct := range accounts {
		out[addr] = cloneHashMap(acct.Storage)
	}
	return out
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
