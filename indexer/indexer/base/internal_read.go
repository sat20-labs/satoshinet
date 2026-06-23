package base

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/db"
)

// GetUtxoInfo returns UTXO data from the current BaseIndexer view.
// It is intended for node-internal reads during block sync/validation.
func (b *BaseIndexer) GetUtxoInfo(utxo string) (*common.UtxoInfo, error) {
	if b == nil {
		return nil, fmt.Errorf("nil base indexer")
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.getUtxoInfo(utxo)
}

func (b *BaseIndexer) getUtxoInfo(utxo string) (*common.UtxoInfo, error) {
	if utxoInfo, ok := b.utxoIndex.Index[utxo]; ok {
		return &common.UtxoInfo{
			UtxoId:   common.GetUtxoId(utxoInfo),
			Value:    utxoInfo.Value,
			PkScript: utxoInfo.Address.PkScript,
			Assets:   utxoInfo.Assets,
		}, nil
	}

	if err := indexer.CheckUtxoFormat(utxo); err != nil {
		return nil, err
	}

	output := &common.UtxoValueInDB{}
	if err := db.GetValueFromDB(db.GetUTXODBKey(utxo), output, b.db); err != nil {
		indexer.Log.Warningf("BaseIndexer->GetUtxoInfo %s failed, %v", utxo, err)
		return nil, err
	}
	if b.isDeletedUtxoID(output.UtxoId) {
		return nil, fmt.Errorf("utxo %s is spent", utxo)
	}

	pkScript, err := b.pkScriptForStoredUtxo(output)
	if err != nil {
		return nil, err
	}
	return &common.UtxoInfo{
		UtxoId:   output.UtxoId,
		Value:    output.Value,
		PkScript: pkScript,
		Assets:   output.Assets,
	}, nil
}

// GetUTXOs returns address UTXOs from the current BaseIndexer address view.
func (b *BaseIndexer) GetUTXOs(address string) (map[uint64]int64, error) {
	if b == nil {
		return nil, fmt.Errorf("nil base indexer")
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()

	addrValue, err := b.getUtxosWithAddress(address)
	if err != nil {
		return nil, err
	}
	utxos := make(map[uint64]int64, len(addrValue.Utxos))
	for id, value := range addrValue.Utxos {
		utxos[id] = value
	}
	return utxos, nil
}

func (b *BaseIndexer) getUtxosWithAddress(address string) (*indexer.AddressValueV2, error) {
	addressValueInDB := b.getAddressValue2(address, b.db)
	if addressValueInDB == nil {
		return nil, fmt.Errorf("not found")
	}
	return addressValueInDB, nil
}

func (b *BaseIndexer) getAddressValue2(address string, ldb indexer.KVDB) *indexer.AddressValueV2 {
	value, ok := b.addressValueMap[address]
	if !ok {
		data, err := db.GetAddressDataFromDBV2(ldb, address)
		if err == nil {
			value = indexer.ToAddressValueV2(data)
			b.addressValueMap[address] = value
		}
	}
	return value
}

// GetUtxoByID resolves a UTXO id against the DB and current in-memory UTXOs.
func (b *BaseIndexer) GetUtxoByID(id uint64) (string, error) {
	if b == nil {
		return "", fmt.Errorf("nil base indexer")
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	utxo, err := db.GetUtxoByID(b.db, id)
	if err != nil {
		for key, value := range b.utxoIndex.Index {
			if common.GetUtxoId(value) == id {
				return key, nil
			}
		}
		indexer.Log.Errorf("BaseIndexer->GetUtxoByID %d failed, err: %v", id, err)
	}
	return utxo, err
}

// GetAddressByID resolves an address id against the current address cache and DB.
func (b *BaseIndexer) GetAddressByID(id uint64) (string, error) {
	if b == nil {
		return "", fmt.Errorf("nil base indexer")
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	return b.getAddressByID(id)
}

func (b *BaseIndexer) getAddressByID(id uint64) (string, error) {
	for addr, value := range b.addressValueMap {
		if value.AddressId == id {
			return addr, nil
		}
	}

	address, err := db.GetAddressByIDFromDB(b.db, id)
	if err != nil {
		common.Log.Errorf("BaseIndexer->GetAddressByID %d failed, err: %v", id, err)
		return "", err
	}
	return address, nil
}

func (b *BaseIndexer) isDeletedUtxoID(id uint64) bool {
	for _, deleted := range b.delUTXOs {
		if deleted.UtxoId == id {
			return true
		}
	}
	return false
}

func (b *BaseIndexer) pkScriptForStoredUtxo(output *common.UtxoValueInDB) ([]byte, error) {
	addrType := output.AddressType
	reqSig := output.ReqSig
	if addrType == uint16(txscript.MultiSigTy) {
		addresses := make([]string, 0, len(output.AddressIds))
		for _, id := range output.AddressIds {
			addr, err := b.getAddressByID(id)
			if err != nil {
				return nil, err
			}
			addresses = append(addresses, addr)
		}
		return indexer.MultiSigToPkScript(int(reqSig), addresses, b.IsMainnet())
	}
	if addrType == uint16(txscript.NullDataTy) {
		return txscript.NullDataScript(nil)
	}

	addr, err := b.getAddressByID(output.AddressIds[0])
	if err != nil {
		return nil, err
	}
	return addressToPkScript(addr, b.IsMainnet())
}
