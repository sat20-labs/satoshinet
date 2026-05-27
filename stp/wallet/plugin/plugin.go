package plugin

import (
	"fmt"
	"os"

	walletcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/config"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

var mgr *wallet.Manager

func InitWalletMgr(dbPath string) error {
	if mgr != nil {
		return nil
	}
	lcfg, err := config.InitConfig()
	if err != nil {
		return fmt.Errorf("InitConfig failed, %v", err)
	}
	if dbPath != "" {
		lcfg.DB = dbPath
	}
	if mnemonic := firstEnv("SATOSHINET_WALLET_MNEMONIC", "SATOSHINET_RPCTEST_STP_MNEMONIC"); mnemonic != "" {
		lcfg.Wallet.Mnemonic = mnemonic
	}
	if password := firstEnv("SATOSHINET_WALLET_PASSWORD", "SATOSHINET_RPCTEST_STP_PASSWORD"); password != "" {
		lcfg.Wallet.Password = password
	} else if os.Getenv("SATOSHINET_RPCTEST_STP_MNEMONIC") != "" {
		lcfg.Wallet.Password = "rpctest"
	}
	wallet.InitLog(lcfg)

	db := wallet.NewKVDB(lcfg.DB + "/db/stp/" + lcfg.Chain)
	if db == nil {
		return fmt.Errorf("NewKVDB %s failed", lcfg.DB)
	}
	mgr = wallet.NewManager(lcfg, db)
	if mgr == nil {
		return fmt.Errorf("NewManager failed")
	}
	if lcfg.Wallet.PSFile != "" {
		pw, err := wallet.LoadPassword(lcfg.DB + "/" + lcfg.Wallet.PSFile)
		if err == nil {
			_, _ = mgr.UnlockWallet(pw)
		}
	}
	if mgr.GetWallet() == nil && lcfg.Wallet.Mnemonic != "" && lcfg.Wallet.Password != "" {
		_, err = mgr.ImportWallet(lcfg.Wallet.Mnemonic, lcfg.Wallet.Password)
		if err != nil {
			return err
		}
	}
	return nil
}

func StartWalletMgr() error {
	if mgr == nil {
		return fmt.Errorf("Wallet manager not init")
	}
	mgr.Start()
	return nil
}

func ReleaseWalletMgr() {
	if mgr != nil {
		mgr.Close()
		mgr = nil
	}
}

func SignMsg(msg []byte) ([]byte, error) {
	w, err := currentWallet()
	if err != nil {
		return nil, err
	}
	return w.SignMessage(msg)
}

func SignPsbt_SatsNet(packet *spsbt.Packet) error {
	w, err := currentWallet()
	if err != nil {
		return err
	}
	return w.SignPsbt_SatsNet(packet)
}

func IsWalletExisting() bool {
	return mgr != nil && mgr.IsWalletExist()
}

func IsUnlocked() bool {
	if mgr == nil {
		return false
	}
	return mgr.GetWallet() != nil
}

func UnlockWallet(pw string) error {
	if mgr == nil {
		return fmt.Errorf("Wallet manager not init")
	}
	_, err := mgr.UnlockWallet(pw)
	return err
}

func CreateWallet(pw string) (string, error) {
	if mgr == nil {
		return "", fmt.Errorf("Wallet manager not init")
	}
	_, mn, err := mgr.CreateWallet(pw)
	return mn, err
}

func ImportWallet(mn, pw string) error {
	if mgr == nil {
		return fmt.Errorf("Wallet manager not init")
	}
	_, err := mgr.ImportWallet(mn, pw)
	return err
}

func GetPubKey() ([]byte, error) {
	w, err := currentWallet()
	if err != nil {
		return nil, err
	}
	return w.GetPaymentPubKey().SerializeCompressed(), nil
}

func currentWallet() (walletcommon.Wallet, error) {
	if mgr == nil {
		return nil, fmt.Errorf("Wallet manager not init")
	}
	w := mgr.GetWallet()
	if w == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked/connected")
	}
	return w, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
