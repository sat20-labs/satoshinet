//go:build rpctest && !stp_source && !stp_plugin && !wallet_source && !wallet_plugin
// +build rpctest,!stp_source,!stp_plugin,!wallet_source,!wallet_plugin

package stp

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/tyler-smith/go-bip39"
)

const (
	rpctestBootstrapMnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
	rpctestCoreMnemonic      = "uniform bulb body vital later special era tourist build chief devote annual"
	rpctestMinerMnemonic     = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
)

var (
	rpctestWalletMu  sync.Mutex
	rpctestWalletKey *btcec.PrivateKey
)

func LoadSTP(dbPath string) error {
	return ensureRPCTestWallet()
}

func StartSTP() error {
	return ensureRPCTestWallet()
}

func ReleaseSTP() {}

func SignMsg(msg []byte) ([]byte, error) {
	if err := ensureRPCTestWallet(); err != nil {
		return nil, err
	}
	rpctestWalletMu.Lock()
	key := rpctestWalletKey
	rpctestWalletMu.Unlock()
	if key == nil {
		return nil, fmt.Errorf("rpctest stp wallet is not initialized")
	}
	return ecdsa.Sign(key, chainhash.HashB(msg)).Serialize(), nil
}

func IsWalletExists() bool {
	return true
}

func IsUnlocked() bool {
	return true
}

func CreateWallet(pw string) (string, error) {
	mnemonic := rpctestMnemonic()
	return mnemonic, setRPCTestMnemonic(mnemonic)
}

func UnlockWallet(pw string) error {
	return ensureRPCTestWallet()
}

func ImportWallet(mn, pw string) error {
	return setRPCTestMnemonic(mn)
}

func GetPubKey() ([]byte, error) {
	if err := ensureRPCTestWallet(); err != nil {
		return nil, err
	}
	rpctestWalletMu.Lock()
	key := rpctestWalletKey
	rpctestWalletMu.Unlock()
	if key == nil {
		return nil, fmt.Errorf("rpctest stp wallet is not initialized")
	}
	return key.PubKey().SerializeCompressed(), nil
}

func ensureRPCTestWallet() error {
	rpctestWalletMu.Lock()
	initialized := rpctestWalletKey != nil
	rpctestWalletMu.Unlock()
	if initialized {
		return nil
	}
	return setRPCTestMnemonic(rpctestMnemonic())
}

func setRPCTestMnemonic(mnemonic string) error {
	key, err := rpctestKeyFromMnemonic(mnemonic, 0)
	if err != nil {
		return err
	}
	rpctestWalletMu.Lock()
	rpctestWalletKey = key
	rpctestWalletMu.Unlock()
	return nil
}

func rpctestMnemonic() string {
	if mnemonic := strings.TrimSpace(os.Getenv("SATOSHINET_RPCTEST_STP_MNEMONIC")); mnemonic != "" {
		return mnemonic
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SATOSHINET_RPCTEST_NODE_ROLE"))) {
	case "core", "corenode":
		return rpctestCoreMnemonic
	case "miner":
		return rpctestMinerMnemonic
	default:
		return rpctestBootstrapMnemonic
	}
}

func rpctestKeyFromMnemonic(mnemonic string, index uint32) (*btcec.PrivateKey, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid rpctest mnemonic")
	}
	seed := bip39.NewSeed(mnemonic, "")
	key, err := hdkeychain.NewMaster(seed, &chaincfg.TestNetParams)
	if err != nil {
		return nil, err
	}
	for _, child := range []uint32{
		hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		0,
		index,
	} {
		key, err = key.Derive(child)
		if err != nil {
			return nil, err
		}
	}
	return key.ECPrivKey()
}
