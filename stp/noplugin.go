//go:build !rpctest && !stp_source && !stp_plugin && !wallet_source && !wallet_plugin

package stp

import (
	"fmt"
)

func LoadSTP(dbPath string) error {
	return nil
}

func StartSTP() error {
	return nil
}

func ReleaseSTP() {
}

func SignMsg(msg []byte) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func IsWalletExists() bool {
	return false
}

func IsUnlocked() bool {
	return false
}

func CreateWallet(pw string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func UnlockWallet(pw string) error {
	return fmt.Errorf("not implemented")
}

func ImportWallet(mn, pw string) error {
	return fmt.Errorf("not implemented")
}

func GetPubKey() ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
