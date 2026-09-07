//go:build stp_plugin

package stp

import (
	"fmt"
	"log"
	"plugin"
	"sync"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

var _stpMgr *plugin.Plugin
var messageServiceMu sync.RWMutex
var messageServiceHandler func([]byte) ([]byte, error)

func registerMessageServiceHandlerWithPlugin(p *plugin.Plugin, handler func([]byte) ([]byte, error)) error {
	if p == nil || handler == nil {
		return nil
	}
	symbol, err := p.Lookup("RegisterMessageServiceHandler")
	if err != nil {
		return err
	}
	f, ok := symbol.(func(func([]byte) ([]byte, error)))
	if !ok {
		return fmt.Errorf("RegisterMessageServiceHandler symbol type assertion failed")
	}
	f(handler)
	return nil
}

func RegisterMessageServiceHandler(handler func([]byte) ([]byte, error)) {
	messageServiceMu.Lock()
	messageServiceHandler = handler
	p := _stpMgr
	messageServiceMu.Unlock()
	if p != nil && handler != nil {
		if err := registerMessageServiceHandlerWithPlugin(p, handler); err != nil {
			log.Printf("register message service handler failed: %v", err)
		}
	}
}

func LoadSTP(dbPath string) error {
	if _stpMgr != nil {
		return nil
	}

	// 打开插件文件
	p, err := plugin.Open("./stpd.so")
	if err != nil {
		log.Printf("plugin.Open failed. %v", err)
		return err
	}

	symbol, err := p.Lookup("InitSTP")
	if err != nil {
		log.Printf("Lookup InitSTP failed: %v", err)
		return err
	}

	initSTP, ok := symbol.(func(string) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	err = initSTP(dbPath)
	if err != nil {
		log.Printf("initSTP failed: %v", err)
		return err
	}

	_stpMgr = p
	messageServiceMu.RLock()
	handler := messageServiceHandler
	messageServiceMu.RUnlock()
	if err := registerMessageServiceHandlerWithPlugin(p, handler); err != nil {
		_stpMgr = nil
		return err
	}
	return nil
}

func StartSTP() error {
	if _stpMgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("StartSTP")
	if err != nil {
		log.Printf("Lookup StartSTP failed: %v", err)
		return err
	}

	f, ok := symbol.(func() error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	err = f()
	if err != nil {
		log.Printf("StartSTP failed, %v", err)
		return err
	}
	log.Printf("StartSTP completed")
	return nil
}

func ReleaseSTP() {
	if _stpMgr == nil {
		return
	}

	symbol, err := _stpMgr.Lookup("ReleaseSTP")
	if err != nil {
		log.Printf("Lookup ReleaseSTP failed: %v", err)
		return
	}

	releaseSTP, ok := symbol.(func())
	if !ok {
		log.Printf("symbol type assertion failed")
		return
	}

	releaseSTP()

	_stpMgr = nil
}

func SignMsg(msg []byte) ([]byte, error) {
	if _stpMgr == nil {
		return nil, fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("SignMsg")
	if err != nil {
		log.Printf("Lookup SignMsg failed: %v", err)
		return nil, err
	}

	signMsg, ok := symbol.(func([]byte) ([]byte, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return nil, fmt.Errorf("symbol type assertion failed")
	}

	return signMsg(msg)
}

func SignPsbt_SatsNet(packet *spsbt.Packet) error {
	if _stpMgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("SignPsbt_SatsNet")
	if err != nil {
		log.Printf("Lookup SignPsbt_SatsNet failed: %v", err)
		return err
	}

	signPsbt, ok := symbol.(func(*spsbt.Packet) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return signPsbt(packet)
}

func IsWalletExists() bool {
	if _stpMgr == nil {
		return false
	}

	symbol, err := _stpMgr.Lookup("IsWalletExisting")
	if err != nil {
		log.Printf("Lookup IsWalletExisting failed: %v", err)
		return false
	}

	isWalletExisting, ok := symbol.(func() bool)
	if !ok {
		log.Printf("symbol type assertion failed")
		return false
	}

	return isWalletExisting()
}

func IsUnlocked() bool {
	if _stpMgr == nil {
		return false
	}

	symbol, err := _stpMgr.Lookup("IsUnlocked")
	if err != nil {
		log.Printf("Lookup IsUnlocked failed: %v", err)
		return false
	}

	isUnlocked, ok := symbol.(func() bool)
	if !ok {
		log.Printf("symbol type assertion failed")
		return false
	}

	return isUnlocked()
}

func CreateWallet(pw string) (string, error) {
	if _stpMgr == nil {
		return "", fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("CreateWallet")
	if err != nil {
		log.Printf("Lookup CreateWallet failed: %v", err)
		return "", err
	}

	f, ok := symbol.(func(string) (string, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return "", fmt.Errorf("symbol type assertion failed")
	}

	return f(pw)
}

func UnlockWallet(pw string) error {
	if _stpMgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("UnlockWallet")
	if err != nil {
		log.Printf("Lookup UnlockWallet failed: %v", err)
		return err
	}

	f, ok := symbol.(func(string) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return f(pw)
}

func ImportWallet(mn, pw string) error {
	if _stpMgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("ImportWallet")
	if err != nil {
		log.Printf("Lookup ImportWallet failed: %v", err)
		return err
	}

	f, ok := symbol.(func(string, string) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return f(mn, pw)
}

func GetPubKey() ([]byte, error) {
	if _stpMgr == nil {
		return nil, fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpMgr.Lookup("GetPubKey")
	if err != nil {
		log.Printf("Lookup GetPubKey failed: %v", err)
		return nil, err
	}

	getPubKey, ok := symbol.(func() ([]byte, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return nil, fmt.Errorf("symbol type assertion failed")
	}

	return getPubKey()
}
