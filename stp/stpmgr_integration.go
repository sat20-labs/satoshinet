//go:build !stp_plugin

package stp

import (
	"log"
	"github.com/sat20-labs/satoshinet/stp/transcend/plugin"
)

func LoadSTP(dbPath string) error {

	err := plugin.InitSTP(dbPath)
	if err != nil {
		log.Printf("initSTP failed: %v", err)
		return err
	}
	

	return nil
}


func StartSTP() error {
	err := plugin.StartSTP()
	if err != nil {
		log.Printf("StartSTP failed, %v", err)
		return err
	}
	return nil
}

func ReleaseSTP() {
	plugin.ReleaseSTP()
}

func SignMsg(msg []byte) ([]byte, error) {

	return plugin.SignMsg(msg)
}

func IsWalletExists() (bool) {
	return plugin.IsWalletExisting()
}


func IsUnlocked() (bool) {
	return plugin.IsUnlocked()
}


func CreateWallet(pw string) (string, error) {

	return plugin.CreateWallet(pw)
}

func UnlockWallet(pw string) (error) {

	return plugin.UnlockWallet(pw)
}

func ImportWallet(mn, pw string) (error) {

	return plugin.ImportWallet(mn, pw)
}

func GetPubKey() ([]byte, error) {
	return plugin.GetPubKey()
}
