package evm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

// Historical JSON balance format exists only in this benchmark fixture.
func marshalEVMJSONBalanceBaseline(s *MemoryStateDB) ([]byte, error) {
	if s == nil {
		s = NewMemoryStateDB()
	}
	var buf bytes.Buffer
	buf.Write(stateCodecMagic)
	buf.WriteByte(1)

	addresses := sortedStateAddresses(s.accounts)
	writeUvarint(&buf, uint64(len(addresses)))
	for _, addr := range addresses {
		acct := s.accounts[addr]
		if acct == nil {
			return nil, fmt.Errorf("nil EVM account %s", addr)
		}
		if err := acct.DeployFlags.Validate(); err != nil {
			return nil, err
		}
		buf.Write(addr.Bytes())
		writeUvarint(&buf, acct.Nonce)
		writeBytes(&buf, acct.Balance.Bytes())
		writeBytes(&buf, acct.Code)
		writeBytes(&buf, []byte(acct.DeployerAddr))
		writeUvarint(&buf, uint64(acct.DeployFlags))
		managed, err := acct.Managed.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("encode EVM managed balance %s: %w", addr, err)
		}
		writeBytes(&buf, managed)
		if acct.Closed {
			buf.WriteByte(1)
		} else {
			buf.WriteByte(0)
		}

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

func decodeEVMJSONBalanceBaseline(data []byte) (*MemoryStateDB, error) {
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
	if version != 1 {
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
		if state.account(addr) != nil {
			return nil, fmt.Errorf("duplicate EVM account %s", addr)
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
		if len(balanceBytes) > 32 {
			return nil, fmt.Errorf("account balance %d overflows uint256", i)
		}
		acct.Balance = *new(uint256.Int).SetBytes(balanceBytes)
		code, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode account code %d: %w", i, err)
		}
		acct.Code = code
		deployerAddr, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode account deployer address %d: %w", i, err)
		}
		acct.DeployerAddr = string(deployerAddr)
		flags, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("decode account deployment flags %d: %w", i, err)
		}
		if flags > uint64(^uint32(0)) {
			return nil, fmt.Errorf("account deployment flags %d overflow uint32", i)
		}
		acct.DeployFlags = ContractFlags(flags)
		if err := acct.DeployFlags.Validate(); err != nil {
			return nil, err
		}
		managed, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode account managed balance %d: %w", i, err)
		}
		if err := acct.Managed.UnmarshalJSON(managed); err != nil {
			return nil, fmt.Errorf("decode account managed balance %d: %w", i, err)
		}
		closed, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("decode account closed %d: %w", i, err)
		}
		if closed > 1 {
			return nil, fmt.Errorf("invalid EVM closed flag %d", closed)
		}
		acct.Closed = closed == 1

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
	triggerCount, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("decode trigger count: %w", err)
	}
	for i := uint64(0); i < triggerCount; i++ {
		prefixBytes, err := readBytes(r)
		if err != nil {
			return nil, fmt.Errorf("decode trigger contract prefix %d: %w", i, err)
		}
		contractVersion, err := r.ReadByte()
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
		contract, err := NewContractAddress(string(prefixBytes), contractVersion, contractType, contractHash)
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
	if r.Len() != 0 {
		return nil, errors.New("trailing EVM state bytes")
	}
	return state, nil
}

func TestCompactEVMRejectsJSONBalance(t *testing.T) {
	raw, err := NewMemoryStateDB().MarshalBinary()
	require.NoError(t, err)
	require.EqualValues(t, 1, raw[len(stateCodecMagic)])
	_, err = DecodeMemoryStateDB(raw)
	require.NoError(t, err)
	// An empty store has no embedded balance, so use one account to exercise it.
	state := NewMemoryStateDB()
	state.SetNonce(gethcommon.Address{1}, 1, 0)
	legacy, err := marshalEVMJSONBalanceBaseline(state)
	require.NoError(t, err)
	_, err = DecodeMemoryStateDB(legacy)
	require.Error(t, err)
}
func TestCompactEVMStatePerformance(t *testing.T) {
	for _, count := range []int{20, 500, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			state := NewMemoryStateDB()
			for i := 0; i < count; i++ {
				addr := gethcommon.BytesToAddress([]byte{byte(i >> 8), byte(i)})
				state.SetNonce(addr, uint64(i), 0)
				state.SetCode(addr, []byte{0x60, 0x2a}, 0)
				state.SetState(addr, gethcommon.Hash{1}, gethcommon.Hash{2})
				state.AddBalance(addr, uint256.NewInt(1000), 0)
				state.ensure(addr).Managed = contractcommon.ManagedBalance{Value: 1000, Assets: wire.TxAssets{{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test"}, Amount: *scommon.NewDecimal(1000, 6), BindingSat: 2}}}
			}
			compact, err := state.MarshalBinary()
			require.NoError(t, err)
			old, err := marshalEVMJSONBalanceBaseline(state)
			require.NoError(t, err)
			recovered, err := DecodeMemoryStateDB(compact)
			require.NoError(t, err)
			require.Equal(t, state.StateRoot(), recovered.StateRoot())
			require.Equal(t, state.accounts, recovered.accounts)
			baseline, err := decodeEVMJSONBalanceBaseline(old)
			require.NoError(t, err)
			require.Equal(t, recovered.accounts, baseline.accounts)
			for _, codec := range []struct {
				name   string
				encode func() ([]byte, error)
				decode func() error
				size   int
			}{
				{"json_balance", func() ([]byte, error) { return marshalEVMJSONBalanceBaseline(state) }, func() error { _, err := decodeEVMJSONBalanceBaseline(old); return err }, len(old)},
				{"compact", state.MarshalBinary, func() error { _, err := DecodeMemoryStateDB(compact); return err }, len(compact)},
			} {
				for _, op := range []string{"encode", "decode"} {
					result := testing.Benchmark(func(b *testing.B) {
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							var err error
							if op == "encode" {
								_, err = codec.encode()
							} else {
								err = codec.decode()
							}
							if err != nil {
								b.Fatal(err)
							}
						}
					})
					t.Logf("METRIC accounts=%d codec=%s op=%s bytes=%d ns_op=%d B_op=%d allocs_op=%d", count, codec.name, op, codec.size, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
				}
			}
		})
	}
}
