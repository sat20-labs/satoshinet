package evm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

var stateCodecMagic = []byte("EVMSTATE")

const stateCodecVersion byte = 3

func (s *MemoryStateDB) Clone() *MemoryStateDB {
	if s == nil {
		return NewMemoryStateDB()
	}
	return &MemoryStateDB{
		accounts:  cloneAccounts(s.accounts),
		triggers:  cloneTriggers(s.triggers),
		logs:      cloneLogs(s.logs),
		refund:    s.refund,
		snapshots: cloneSnapshots(s.snapshots),
	}
}

func (s *MemoryStateDB) MarshalBinary() ([]byte, error) {
	if s == nil {
		s = NewMemoryStateDB()
	}
	var buf bytes.Buffer
	buf.Write(stateCodecMagic)
	buf.WriteByte(stateCodecVersion)

	addresses := sortedStateAddresses(s.accounts)
	writeUvarint(&buf, uint64(len(addresses)))
	for _, addr := range addresses {
		acct := s.accounts[addr]
		buf.Write(addr.Bytes())
		writeUvarint(&buf, acct.Nonce)
		writeBytes(&buf, acct.Balance.Bytes())
		writeBytes(&buf, acct.Code)

		keys := sortedStorageKeys(acct.Storage)
		writeUvarint(&buf, uint64(len(keys)))
		for _, key := range keys {
			buf.Write(key.Bytes())
			buf.Write(acct.Storage[key].Bytes())
		}
	}

	triggerKeys := sortedTriggerKeys(s.triggers)
	writeUvarint(&buf, uint64(len(triggerKeys)))
	for _, key := range triggerKeys {
		trigger := s.triggers[key]
		contractHash := ContractAddressHash(trigger.Contract)
		writeBytes(&buf, []byte(trigger.Contract.Prefix()))
		buf.WriteByte(trigger.Contract.Version())
		buf.WriteByte(trigger.Contract.ContractType())
		buf.Write(contractHash[:])
		writeBytes(&buf, []byte(trigger.ID))
		buf.WriteByte(byte(trigger.Kind))
		writeVarint64(&buf, trigger.Height)
		writeGasUvarint(&buf, trigger.GasLimit)
		writeBytes(&buf, trigger.Calldata)
	}
	return buf.Bytes(), nil
}

func (s *MemoryStateDB) UnmarshalBinary(data []byte) error {
	decoded, err := DecodeMemoryStateDB(data)
	if err != nil {
		return err
	}
	s.accounts = decoded.accounts
	s.triggers = decoded.triggers
	s.logs = nil
	s.refund = 0
	s.snapshots = nil
	return nil
}

func DecodeMemoryStateDB(data []byte) (*MemoryStateDB, error) {
	r := bytes.NewReader(data)
	magic := make([]byte, len(stateCodecMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, fmt.Errorf("decode state magic: %w", err)
	}
	if !bytes.Equal(magic, stateCodecMagic) {
		return nil, errors.New("invalid EVM state magic")
	}
	version, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("decode state version: %w", err)
	}
	if version != 1 && version != 2 && version != stateCodecVersion {
		return nil, fmt.Errorf("unsupported EVM state version %d", version)
	}

	accountCount, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("decode account count: %w", err)
	}
	state := NewMemoryStateDB()
	for i := uint64(0); i < accountCount; i++ {
		var addr gethcommon.Address
		if _, err := io.ReadFull(r, addr[:]); err != nil {
			return nil, fmt.Errorf("decode account address %d: %w", i, err)
		}
		acct := state.ensure(addr)
		nonce, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("decode account nonce %d: %w", i, err)
		}
		acct.Nonce = nonce
		balanceBytes, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode account balance %d: %w", i, err)
		}
		acct.Balance = *new(uint256.Int).SetBytes(balanceBytes)
		code, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode account code %d: %w", i, err)
		}
		acct.Code = code

		storageCount, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("decode storage count %d: %w", i, err)
		}
		for j := uint64(0); j < storageCount; j++ {
			var key, value gethcommon.Hash
			if _, err := io.ReadFull(r, key[:]); err != nil {
				return nil, fmt.Errorf("decode storage key %d/%d: %w", i, j, err)
			}
			if _, err := io.ReadFull(r, value[:]); err != nil {
				return nil, fmt.Errorf("decode storage value %d/%d: %w", i, j, err)
			}
			if value != (gethcommon.Hash{}) {
				acct.Storage[key] = value
			}
		}
	}
	if version >= 2 {
		triggerCount, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("decode trigger count: %w", err)
		}
		for i := uint64(0); i < triggerCount; i++ {
			prefixBytes, err := readBytes(r)
			if err != nil {
				return nil, fmt.Errorf("decode trigger contract prefix %d: %w", i, err)
			}
			version, err := r.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("decode trigger contract version %d: %w", i, err)
			}
			contractType, err := r.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("decode trigger contract type %d: %w", i, err)
			}
			var contractHash EVMAddress
			if _, err := io.ReadFull(r, contractHash[:]); err != nil {
				return nil, fmt.Errorf("decode trigger contract %d: %w", i, err)
			}
			idBytes, err := readBytes(r)
			if err != nil {
				return nil, fmt.Errorf("decode trigger id %d: %w", i, err)
			}
			kind, err := r.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("decode trigger kind %d: %w", i, err)
			}
			height, err := readVarint64(r)
			if err != nil {
				return nil, fmt.Errorf("decode trigger height %d: %w", i, err)
			}
			if version == 2 {
				if _, err := readVarint64(r); err != nil {
					return nil, fmt.Errorf("decode trigger time %d: %w", i, err)
				}
			}
			gasLimit, err := binary.ReadUvarint(r)
			if err != nil {
				return nil, fmt.Errorf("decode trigger gas limit %d: %w", i, err)
			}
			gasLimitInt, err := contractframework.GasUnitsInt64(gasLimit)
			if err != nil {
				return nil, fmt.Errorf("decode trigger gas limit %d: %w", i, err)
			}
			calldata, err := readBytes(r)
			if err != nil {
				return nil, fmt.Errorf("decode trigger calldata %d: %w", i, err)
			}
			contract, err := NewContractAddress(string(prefixBytes), version, contractType, contractHash)
			if err != nil {
				return nil, fmt.Errorf("decode trigger contract %d: %w", i, err)
			}
			trigger := Trigger{
				ID:       string(idBytes),
				Contract: contract,
				Kind:     TriggerKind(kind),
				Height:   height,
				GasLimit: gasLimitInt,
				Calldata: calldata,
			}
			if err := state.RegisterTrigger(trigger); err != nil {
				return nil, fmt.Errorf("decode trigger %d: %w", i, err)
			}
		}
	}
	if r.Len() != 0 {
		return nil, errors.New("trailing EVM state bytes")
	}
	return state, nil
}

func cloneSnapshots(src []memorySnapshot) []memorySnapshot {
	if src == nil {
		return nil
	}
	dst := make([]memorySnapshot, len(src))
	for i, snap := range src {
		dst[i] = memorySnapshot{
			accounts: cloneAccounts(snap.accounts),
			triggers: cloneTriggers(snap.triggers),
			logs:     cloneLogs(snap.logs),
			refund:   snap.refund,
		}
	}
	return dst
}

func sortedStateAddresses(accounts map[gethcommon.Address]*memoryAccount) []gethcommon.Address {
	addresses := make([]gethcommon.Address, 0, len(accounts))
	for addr := range accounts {
		addresses = append(addresses, addr)
	}
	sort.Slice(addresses, func(i, j int) bool {
		return bytes.Compare(addresses[i].Bytes(), addresses[j].Bytes()) < 0
	})
	return addresses
}

func sortedStorageKeys(storage map[gethcommon.Hash]gethcommon.Hash) []gethcommon.Hash {
	keys := make([]gethcommon.Hash, 0, len(storage))
	for key := range storage {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare(keys[i].Bytes(), keys[j].Bytes()) < 0
	})
	return keys
}

func writeBytes(w *bytes.Buffer, b []byte) {
	writeUvarint(w, uint64(len(b)))
	w.Write(b)
}

func writeUvarint(w *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	w.Write(tmp[:n])
}

func writeVarint64(w *bytes.Buffer, v int64) {
	writeUvarint(w, uint64(v<<1)^uint64(v>>63))
}

func writeGasUvarint(w *bytes.Buffer, v int64) {
	if v < 0 {
		v = 0
	}
	writeUvarint(w, uint64(v))
}

func readBytes(r *bytes.Reader) ([]byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	if length > uint64(r.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, int(length))
	_, err = io.ReadFull(r, out)
	return out, err
}

func readVarint64(r *bytes.Reader) (int64, error) {
	value, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, err
	}
	return int64(value>>1) ^ -int64(value&1), nil
}
