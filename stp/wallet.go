//go:build wallet_plugin

package stp

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"plugin"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

var _walletMgr *plugin.Plugin

func LoadSTP(dbPath string) error {
	if _walletMgr != nil {
		return nil
	}

	exePath, err := os.Executable()
	if err != nil {
		log.Printf("os.Executable failed. %v", err)
		return err
	}
	pluginPath := filepath.Join(filepath.Dir(exePath), "wallet.so")

	// 打开插件文件
	p, err := plugin.Open(pluginPath)
	if err != nil {
		log.Printf("plugin.Open failed. %v", err)
		return err
	}

	symbol, err := p.Lookup("InitWalletMgr")
	if err != nil {
		log.Printf("Lookup InitWalletMgr failed: %v", err)
		return err
	}

	f, ok := symbol.(func(string) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	err = f(dbPath)
	if err != nil {
		log.Printf("InitWalletMgr failed: %v", err)
		return err
	}

	_walletMgr = p
	return nil
}

func StartSTP() error {
	if _walletMgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _walletMgr.Lookup("StartWalletMgr")
	if err != nil {
		log.Printf("Lookup StartWalletMgr failed: %v", err)
		return err
	}

	f, ok := symbol.(func() error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	err = f()
	if err != nil {
		log.Printf("StartWalletMgr failed, %v", err)
		return err
	}
	log.Printf("StartWalletMgr completed")
	return nil
}

func ReleaseSTP() {
	if _walletMgr == nil {
		return
	}

	symbol, err := _walletMgr.Lookup("ReleaseWalletMgr")
	if err != nil {
		log.Printf("Lookup ReleaseWalletMgr failed: %v", err)
		return
	}

	f, ok := symbol.(func())
	if !ok {
		log.Printf("symbol type assertion failed")
		return
	}

	f()

	_walletMgr = nil
}

func SignMsg(msg []byte) ([]byte, error) {
	if _walletMgr == nil {
		return nil, fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("SignMsg")
	if err != nil {
		log.Printf("Lookup SignMsg failed: %v", err)
		return nil, err
	}

	f, ok := symbol.(func([]byte) ([]byte, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return nil, fmt.Errorf("symbol type assertion failed")
	}

	return f(msg)
}

func SignPsbt_SatsNet(packet *spsbt.Packet) error {
	if _walletMgr == nil {
		return fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("SignPsbt_SatsNet")
	if err != nil {
		log.Printf("Lookup SignPsbt_SatsNet failed: %v", err)
		return err
	}

	f, ok := symbol.(func(*spsbt.Packet) error)
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return f(packet)
}

func IsWalletExists() bool {
	if _walletMgr == nil {
		return false
	}

	symbol, err := _walletMgr.Lookup("IsWalletExisting")
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
	if _walletMgr == nil {
		return false
	}

	symbol, err := _walletMgr.Lookup("IsUnlocked")
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
	if _walletMgr == nil {
		return "", fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("CreateWallet")
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
	if _walletMgr == nil {
		return fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("UnlockWallet")
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
	if _walletMgr == nil {
		return fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("ImportWallet")
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
	if _walletMgr == nil {
		return nil, fmt.Errorf("WalletManager not init")
	}

	symbol, err := _walletMgr.Lookup("GetPubKey")
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

func RegisterMessageServiceHandler(handler func([]byte) ([]byte, error)) {}
