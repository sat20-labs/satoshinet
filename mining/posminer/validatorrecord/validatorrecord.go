package validatorrecord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	fileName = "validatorRecord.json"
)

type ValidatorRecord struct {
	ValidatorId       string    `json:"validatorId"`       // 验证者的ID, pubkey
	LastConnectedTime time.Time `json:"lastConnectedTime"` // 上次连接的时间
}

type ValidatorRecordMgr struct {
	filepath string

	ValidatorRecordMap map[string]*ValidatorRecord // Host 
}

func LoadValidatorRecordList(path string) *ValidatorRecordMgr {

	validatorRecordMgr := &ValidatorRecordMgr{}

	validatorRecordMgr.filepath = filepath.Join(path, fileName)
	validatorRecordMgr.ValidatorRecordMap, _ = loadValidatorRecordFile(validatorRecordMgr.filepath)

	return validatorRecordMgr
}

func loadValidatorRecordFile(filepath string) (map[string]*ValidatorRecord, error) {

	data, err := loadValidatorKeyData(filepath)
	if err != nil {
		// Local key failed, will create a new key
		err := fmt.Errorf("open validator record file failed: %s", err.Error())
		return nil, err
	}

	validatorRecordList := make(map[string]*ValidatorRecord, 0)
	err = json.Unmarshal(data, &validatorRecordList)
	if err != nil {
		err := fmt.Errorf("unmarshal validatorRecordList failed: %s", err.Error())
		return nil, err
	}

	return validatorRecordList, nil
}

func UpdateValidatorRecordList(filepath string, validaterRecordList map[string]*ValidatorRecord) error {
	data, err := json.Marshal(validaterRecordList)
	if err != nil {
		err := fmt.Errorf("marshal validatorRecordList failed: %s", err.Error())
		return err
	}
	err = saveValidatorRecordFile(filepath, data)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	return nil
}

func loadValidatorKeyData(path string) ([]byte, error) {
	entory, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return entory, nil
}

func saveValidatorRecordFile(path string, data []byte) error {
	err := os.WriteFile(path, data, 0600)
	if err != nil {
		return err
	}
	return nil
}

func (vrm *ValidatorRecordMgr) UpdateValidatorRecord(pubkey string, host string) {
	// Update validator record when the validator is connected

	if vrm.ValidatorRecordMap == nil {
		vrm.ValidatorRecordMap = make(map[string]*ValidatorRecord, 0)
	}

	if host == "127.0.0.1" {
		return 
	}

	record, ok := vrm.ValidatorRecordMap[host]
	if ok {
		record.ValidatorId = pubkey
		record.LastConnectedTime = time.Now()
	} else {
		record = &ValidatorRecord{
			ValidatorId:       pubkey,
			LastConnectedTime: time.Now(),
		}
		vrm.ValidatorRecordMap[host] = record
	}

	UpdateValidatorRecordList(vrm.filepath, vrm.ValidatorRecordMap)
}
