package evm

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func (s *MemoryStateDB) StateRoot() [32]byte {
	h := sha256.New()
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
		binary.BigEndian.PutUint64(tmp[:], trigger.GasLimit)
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
