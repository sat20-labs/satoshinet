//go:build wallet_source

package stp

import (
	"log"
	"github.com/sat20-labs/satoshinet/stp/wallet/plugin"
)


/*
非plugin模式，需要将sat20wallet/sdk的所有代码，copy到./sdk，然后编译。用于不支持plugin的平台，比如windows。
*/


func LoadSTP(dbPath string) error {

	err := plugin.InitWalletMgr(dbPath)
	if err != nil {
		log.Printf("InitWalletMgr failed: %v", err)
		return err
	}
	

	return nil
}


func StartSTP() error {
	err := plugin.StartWalletMgr()
	if err != nil {
		log.Printf("StartWalletMgr failed, %v", err)
		return err
	}
	return nil
}

func ReleaseSTP() {
	plugin.ReleaseWalletMgr()
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
