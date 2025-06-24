//go:build stp_plugin

package stp

import (
	"fmt"
	"log"
	"plugin"
)

var _stpmar *plugin.Plugin

func LoadSTP(dbPath string) error {
	if _stpmar != nil {
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

	_stpmar = p
	return nil
}


func StartSTP() error {
	if _stpmar == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("StartSTP")
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
	if _stpmar == nil {
		return 
	}

	symbol, err := _stpmar.Lookup("ReleaseSTP")
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

	_stpmar = nil
}


func SignMsg(msg []byte) ([]byte, error) {
	if _stpmar == nil {
		return nil, fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("SignMsg")
	if err != nil {
		log.Printf("Lookup SignMsg failed: %v", err)
		return  nil, err
	}

	signMsg, ok := symbol.(func([]byte) ([]byte, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return nil, fmt.Errorf("symbol type assertion failed")
	}

	return signMsg(msg)
}

func IsWalletExists() (bool) {
	if _stpmar == nil {
		return false
	}

	symbol, err := _stpmar.Lookup("IsWalletExisting")
	if err != nil {
		log.Printf("Lookup IsWalletExisting failed: %v", err)
		return  false
	}

	isWalletExisting, ok := symbol.(func() (bool))
	if !ok {
		log.Printf("symbol type assertion failed")
		return false
	}

	return isWalletExisting()
}


func IsUnlocked() (bool) {
	if _stpmar == nil {
		return false
	}

	symbol, err := _stpmar.Lookup("IsUnlocked")
	if err != nil {
		log.Printf("Lookup IsUnlocked failed: %v", err)
		return  false
	}

	isUnlocked, ok := symbol.(func() (bool))
	if !ok {
		log.Printf("symbol type assertion failed")
		return false
	}

	return isUnlocked()
}


func CreateWallet(pw string) (string, error) {
	if _stpmar == nil {
		return "", fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("CreateWallet")
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

func UnlockWallet(pw string) (error) {
	if _stpmar == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("UnlockWallet")
	if err != nil {
		log.Printf("Lookup UnlockWallet failed: %v", err)
		return err
	}

	f, ok := symbol.(func(string) (error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return f(pw)
}

func ImportWallet(mn, pw string) (error) {
	if _stpmar == nil {
		return fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("ImportWallet")
	if err != nil {
		log.Printf("Lookup ImportWallet failed: %v", err)
		return err
	}

	f, ok := symbol.(func(string, string) (error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return fmt.Errorf("symbol type assertion failed")
	}

	return f(mn, pw)
}

func GetPubKey() ([]byte, error) {
	if _stpmar == nil {
		return nil, fmt.Errorf("STPManager not init")
	}

	symbol, err := _stpmar.Lookup("GetPubKey")
	if err != nil {
		log.Printf("Lookup GetPubKey failed: %v", err)
		return  nil, err
	}

	getPubKey, ok := symbol.(func() ([]byte, error))
	if !ok {
		log.Printf("symbol type assertion failed")
		return nil, fmt.Errorf("symbol type assertion failed")
	}

	return getPubKey()
}
