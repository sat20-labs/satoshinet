//go:build stp_source

package stp

import (
	"log"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	"github.com/sat20-labs/satoshinet/stp/transcend/plugin"
)

/*
非plugin模式，需要将transcend所有代码，copy到本目录，然后编译。用于不支持plugin的平台，比如windows。
*/

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

func SignPsbt_SatsNet(packet *spsbt.Packet) error {
	return plugin.SignPsbt_SatsNet(packet)
}

func IsWalletExists() bool {
	return plugin.IsWalletExisting()
}

func IsUnlocked() bool {
	return plugin.IsUnlocked()
}

func CreateWallet(pw string) (string, error) {

	return plugin.CreateWallet(pw)
}

func UnlockWallet(pw string) error {

	return plugin.UnlockWallet(pw)
}

func ImportWallet(mn, pw string) error {

	return plugin.ImportWallet(mn, pw)
}

func GetPubKey() ([]byte, error) {
	return plugin.GetPubKey()
}

func RegisterMessageServiceHandler(handler func([]byte) ([]byte, error)) {
	plugin.RegisterMessageServiceHandler(handler)
}
