package btcwallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	sync "sync"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/btcutil/psbt"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/tyler-smith/go-bip39"
)

const (
	CHANNEL_INTERNAL = 1
	CHANNEL_EXTERNAL = 0

	DEFAULT_ADDRESS = 3

	AMT_ALL = 100000000
)

const (
	BTC_ADDRESS_P2PKH             uint32 = 0 //  Legacy
	BTC_ADDRESS_P2SH              uint32 = 1 //  NestedSegwit
	BTC_ADDRESS_P2WKH             uint32 = 2 //  NativeSegwit
	BTC_ADDRESS_P2WSH             uint32 = 3 //  P2WSH
	BTC_ADDRESS_P2TR              uint32 = 4 //  Taproot
	BTC_ADDRESS_INSCRIPTIONCOMMIT uint32 = 5 //  Inscription Commit Tx address

	BTC_ADDRESS_UNKNOWN uint32 = 0xffffffff // Unknow type

	BTC_WalletInfo_Templete = "/%s/%s/%s/walletinfo" // /mtv/userid/btcaccountname/walletinfo
)

type OrdxAssetsInfo struct {
	Ticker  string
	Balance int64
}

type OrdxAssets struct {
	Ticker string
	utxos  map[wire.OutPoint]*utxo
}
type BTCAddressInfo struct {
	Channel      uint32 `protobuf:"varint,1,opt,name=Channel,proto3" json:"Channel,omitempty"`
	Index        uint32 `protobuf:"varint,2,opt,name=Index,proto3" json:"Index,omitempty"`
	AddressType  uint32 `protobuf:"varint,3,opt,name=AddressType,proto3" json:"AddressType,omitempty"`
	AddressAlias string `protobuf:"bytes,4,opt,name=AddressAlias,proto3" json:"AddressAlias,omitempty"`
}
type BTCWalletInfo struct {
	Entropy           []byte            `protobuf:"bytes,1,opt,name=Entropy,proto3" json:"Entropy,omitempty"`
	ChangeAddress     string            `protobuf:"bytes,2,opt,name=ChangeAddress,proto3" json:"ChangeAddress,omitempty"`
	AddressInfoList   []*BTCAddressInfo `protobuf:"bytes,3,rep,name=AddressInfoList,proto3" json:"AddressInfoList,omitempty"`
	CurrentIndex      uint32            `protobuf:"varint,4,opt,name=CurrentIndex,proto3" json:"CurrentIndex,omitempty"`
	DefaultPayAddress string            `protobuf:"bytes,5,opt,name=DefaultPayAddress,proto3" json:"DefaultPayAddress,omitempty"`
}

type BTCAddressDetails struct {
	// Address meta
	BTCAddressInfo

	// Address info
	address       string
	chainAmount   *big.Float
	mempoolAmount *big.Float

	// Address content
	addresskey *hdkeychain.ExtendedKey
	utxos      map[wire.OutPoint]*utxo

	// ordx assets
	ordxAssets []*OrdxAssets
}

type BTCWallet struct {
	walletAddr []byte
	walletName string

	// Wallet info metadata, it will be stored in dkvs
	WalletInfo *BTCWalletInfo

	sync.RWMutex

	masterKey *hdkeychain.ExtendedKey

	addressList []*BTCAddressDetails
}

type BTCAddressSummary struct {
	Address       string `json:"Address,omitempty"`
	Amount        string `json:"Amount,omitempty"`
	MempoolAmount string `json:"MempoolAmount,omitempty"`
	Channel       uint32 `json:"Channel,omitempty"`
	Index         uint32 `json:"Index,omitempty"`
	AddressType   uint32 `json:"AddressType,omitempty"`
	AddressAlias  string `json:"AddressAlias,omitempty"`
	Path          string `json:"Path,omitempty"`
}

type BTCAccountDetails struct {
	// Address summary
	ExternalAddresses []*BTCAddressSummary `json:"ExternalAddresses,omitempty"`
	InternalAddresses []*BTCAddressSummary `json:"InternalAddresses,omitempty"`

	AddressCount       int    `json:"AddressCount,omitempty"`
	TotalAmount        string `json:"TotalAmount,omitempty"`
	TotalMempoolAmount string `json:"TotalMempoolAmount,omitempty"`

	ChangeAddress     string `json:"ChangeAddress,omitempty"`
	DefaultPayAddress string `json:"DefaultPayAddress,omitempty"`
}

func InitBTCWallet(walletname string, workDir string) (*BTCWallet, error) {
	fmt.Printf("InitBTCWallet:%s ... \n", walletname)
	wallet := &BTCWallet{}

	//userAccount := useraccountmanager.GetInstance
	walletAddr, err := GetUserAccountID(walletname)
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}
	wallet.walletAddr = walletAddr
	wallet.walletName = walletname
	fmt.Printf("Wallet key: %x\n", walletAddr)

	//wallet.currentIndex = 0
	//wallet.defaultAddress = DEFAULT_ADDRESS

	masterKey, err := wallet.getMasterKey(walletname)
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}
	wallet.masterKey = masterKey

	err = wallet.loadWalletInfo()
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}
	wallet.loadWalletAddresses()

	//wallet.changeAddress = ""
	fmt.Println("InitBTCWallet done. ")
	return wallet, nil
}

func CreateBTCWallet(walletname string) (*BTCWallet, error) {
	fmt.Printf("CreateBTCWallet:%s ... \n", walletname)
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	wallet, err := createWithEntropy(walletname, entropy)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	// defaultAddress, err := wallet.GetDefaultAddress()
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return nil, err
	// }
	// err = wallet.SetDefaultAddress(defaultAddress)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return nil, err
	// }

	return wallet, nil
}

func Import(walletname string, mnemonic string) (*BTCWallet, error) {
	fmt.Printf("Import with mnemonic:%s ... \n", walletname)
	entropy, err := bip39.EntropyFromMnemonic(mnemonic)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	return createWithEntropy(walletname, entropy)
}
func ImportWithPrivateKey(walletname string, privateKey string) (*BTCWallet, error) {
	fmt.Printf("ImportWithPrivateKey:%s ... \n", walletname)
	wallet := &BTCWallet{
		walletName: walletname,
	}
	wallet.WalletInfo = &BTCWalletInfo{}

	_, err := GetUserAccountID(walletname)
	if err == nil {
		// wallet account is existed

		err = fmt.Errorf("the wallet %s has existed", walletname)
		fmt.Println(err.Error())
		return nil, err
	}

	privateKeyBytes, err := hex.DecodeString(privateKey)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	privKey, _ := btcec.PrivKeyFromBytes(privateKeyBytes)

	pubKey := privKey.PubKey()

	tapKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	publicBytes := tapKey.SerializeCompressed()
	fmt.Printf("The BTC Wallet tapKey public bytes: %x\n", publicBytes)

	addrScript, err := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(tapKey),
		NetParams,
	)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	address := addrScript.EncodeAddress()
	fmt.Printf("The BTC Wallet Taproot No Script Address (P2Taproot): %s\n", address)

	/*
		parentFP := []byte{0x00, 0x00, 0x00, 0x00}
		wallet.masterKey, err = hdkeychain.NewExtendedKey(NetParams.HDPrivateKeyID[:], privateKeyBytes, privateKeyBytes,
			parentFP, 0, 0, true), nil

		wallet.masterKey, err = hdkeychain.NewMaster(privateKeyBytes, NetParams)
		if err != nil {
			// wallet account is not created
			fmt.Println(err.Error())
			return nil, err
		}

		privateKeyString := wallet.masterKey.String()
		fmt.Println("master private key: %s", privateKeyString)

		publicKey, _ := wallet.masterKey.ECPubKey()

		publicKeyBytes := publicKey.SerializeCompressed()

		userAccount.AddChildAccount([]byte(privateKeyString), publicKeyBytes, walletname, useraccountmanager.ACCOUNT_ETH, useraccountmanager.USERACCOUNT_CATEGORY_BTCWALLET)

		//wallet.defaultAddress = DEFAULT_ADDRESS
		// Add the wallet into eth wallet amanger
		btcWalletMgr := GetWalletInst()
		err = btcWalletMgr.AddBTCWallet(wallet)
		if err != nil {
			fmt.Println(err.Error())
			return nil, err
		}

		wallet.WalletInfo.Entropy = nil
		//wallet.currentIndex = 0
		wallet.WalletInfo.ChangeAddress = ""     // Change to pay address
		wallet.WalletInfo.DefaultPayAddress = "" // Mix pay

		// create default address
		wallet.WalletInfo.AddressInfoList = make([]*BTCAddressInfo, 0)

		// default address : P2PKH (Leagcy address)
		leagcyAddressInfo := &BTCAddressInfo{
			Channel:     CHANNEL_EXTERNAL,
			Index:       0,
			AddressType: uint32(BTC_ADDRESS_P2PKH),
		}
		wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList, leagcyAddressInfo)

		// default address : P2TR
		p2trAddressInfo := &BTCAddressInfo{
			Channel:     CHANNEL_EXTERNAL,
			Index:       0,
			AddressType: uint32(BTC_ADDRESS_P2TR),
		}
		wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList, p2trAddressInfo)

		wallet.UpdateWalletInfo()

		// load wallet addresses
		wallet.loadWalletAddresses()
	*/

	fmt.Println("New BTC Wallet done. ")
	return wallet, nil
}

func createWithEntropy(walletname string, entropy []byte) (*BTCWallet, error) {
	fmt.Printf("CreateWithEntropy:%s... \n", walletname)
	wallet := &BTCWallet{
		walletName: walletname,
	}
	wallet.WalletInfo = &BTCWalletInfo{}

	//userAccount := useraccountmanager.GetInstance()
	_, err := GetUserAccountID(walletname)
	if err == nil {
		// wallet account is existed

		err = fmt.Errorf("the wallet %s has existed", walletname)
		fmt.Println(err.Error())
		return nil, err
	}

	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	fmt.Printf("mnemonic: %s\n", mnemonic)

	seed := bip39.NewSeed(mnemonic, "") //这里可以选择传入指定密码或者空字符串，不同密码生成的助记词不同

	wallet.masterKey, err = hdkeychain.NewMaster(seed, NetParams)
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}

	/*
		wallet.currentIndex = 0
		wallet.addressKey, err = wallet.getAddressKey(wallet.masterKey, CHANNEL_EXTERNAL, wallet.currentIndex)
		if err != nil {
			// wallet account is not created
			fmt.Println(err.Error())
			return nil, err
		}
	*/

	privateKeyString := wallet.masterKey.String()
	fmt.Printf("master private key: %s\n", privateKeyString)

	publicKey, _ := wallet.masterKey.ECPubKey()

	publicKeyBytes := publicKey.SerializeCompressed()

	AddChildAccount(walletname, mnemonic, publicKeyBytes, []byte(privateKeyString))

	//wallet.defaultAddress = DEFAULT_ADDRESS
	// Add the wallet into eth wallet amanger
	btcWalletMgr := GetWalletInst()
	err = btcWalletMgr.AddBTCWallet(wallet)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	wallet.WalletInfo.Entropy = entropy
	//wallet.currentIndex = 0
	wallet.WalletInfo.ChangeAddress = ""     // Change to pay address
	wallet.WalletInfo.DefaultPayAddress = "" // Mix pay

	// create default address
	wallet.WalletInfo.AddressInfoList = make([]*BTCAddressInfo, 0)

	// default address : P2PKH (Leagcy address)
	leagcyAddressInfo := &BTCAddressInfo{
		Channel:     CHANNEL_EXTERNAL,
		Index:       0,
		AddressType: uint32(BTC_ADDRESS_P2PKH),
	}
	wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList, leagcyAddressInfo)

	// default address : P2TR
	p2trAddressInfo := &BTCAddressInfo{
		Channel:     CHANNEL_EXTERNAL,
		Index:       0,
		AddressType: uint32(BTC_ADDRESS_P2TR),
	}
	wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList, p2trAddressInfo)

	wallet.UpdateWalletInfo()

	// load wallet addresses
	wallet.loadWalletAddresses()

	// set default address -- Taproot
	for _, item := range wallet.addressList {
		if item.AddressType == uint32(BTC_ADDRESS_P2TR) {
			wallet.SetDefaultAddress(item.address)
			break
		}
	}

	fmt.Println("New BTC Wallet done. ")
	return wallet, nil
}

func (wallet *BTCWallet) loadWalletAddresses() {
	//wallet.addressMap = make(map[string]*BTCAddressDetails)
	wallet.addressList = make([]*BTCAddressDetails, 0)

	for _, addressInfo := range wallet.WalletInfo.AddressInfoList {
		_, addressDetails, err := wallet.loadBTCAddress(addressInfo.Channel, addressInfo.Index, addressInfo.AddressType, addressInfo.AddressAlias)
		if err == nil {
			//wallet.addressMap[address] = addressDetails
			wallet.addressList = append(wallet.addressList, addressDetails)
		}
	}

}

func (wallet *BTCWallet) loadBTCAddress(channel uint32, index uint32, addresstype uint32, addressAlias string) (string, *BTCAddressDetails, error) {
	addressDetails := &BTCAddressDetails{}

	addressDetails.Channel = channel
	addressDetails.Index = index
	addressDetails.AddressType = uint32(addresstype)
	addressDetails.AddressAlias = addressAlias

	addressKey, err := wallet.getAddressKey(wallet.masterKey, addresstype, channel, index)
	if err != nil {
		return "", nil, err
	}

	addressDetails.addresskey = addressKey

	address, err := wallet.GetAddress(addressKey, addresstype)
	if err != nil {
		return "", nil, err
	}

	addressDetails.address = address

	go func() {
		wallet.refreshBTCAddressDetails(addressDetails)
	}()

	return address, addressDetails, err
}

func (wallet *BTCWallet) refreshBTCAddressDetails(details *BTCAddressDetails) error {
	// The Address details balance and utxo should be refreshed from the chain
	//addressDetails.balance, err = wallet.GetBalance()
	var err error
	details.chainAmount, details.mempoolAmount, err = GetBTCBalance(details.address)
	if err != nil {
		return err
	}

	//utxos, err := getBTCUtoxs(details.address)
	utxos, err := getBTCUtoxsWithBlock(details.address)
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return err
	}

	// select ordx assets
	// utxos, ordxAssets, err := pickOrdxAssets(details.address, utxos)
	// if err != nil {
	// 	// wallet account is not created
	// 	fmt.Println(err.Error())
	// 	return err
	// }
	// save the locked utxo

	actualAmount := btcutil.Amount(0)
	for _, utxo := range utxos {
		actualAmount += utxo.value
	}

	details.chainAmount = big.NewFloat(actualAmount.ToUnit(btcutil.AmountBTC))
	details.utxos = utxos
	//details.ordxAssets = ordxAssets

	fmt.Println("   Address: ", details.address)
	fmt.Println("   Total Amount: ", details.chainAmount)
	fmt.Println("    ----------------------------------------------------------: ")
	// log address utxo
	for op, utxo := range utxos {
		fmt.Println("    utxo: ", op.String())
		fmt.Println("    Value: ", utxo.value)
		logTxAssets("    Utxo Assets ", utxo.txAssets)
		fmt.Println("    ----------------------------------------------------------: ")
	}

	return nil
}

func (wallet *BTCWallet) GetWalletMnemonic() (string, error) {
	if wallet.WalletInfo.Entropy == nil {
		err := errors.New("no entropy for this wallet")
		return "", err
	}

	mnemonic, err := bip39.NewMnemonic(wallet.WalletInfo.Entropy)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	return mnemonic, err
}

func (wallet *BTCWallet) GenerateAddress(channel uint32, addresstype uint32, AddressAlias string) (string, error) {

	if channel != CHANNEL_EXTERNAL && channel != CHANNEL_INTERNAL {
		err := errors.New("invalid channel, it just support 0 -- CHANNEL_EXTERNAL or 1 -- CHANNEL_INTERNAL")
		fmt.Println(err.Error())
		return "", err
	}

	/*
		if index > 255 {
			err := errors.New("invalid index, it just support 0 ~ 255")
			fmt.Println(err.Error())
			return "", err
		}
	*/

	if addresstype > BTC_ADDRESS_P2TR {
		err := errors.New("invalid address type, it just support 0 ~ 4")
		fmt.Println(err.Error())
		return "", err

	}

	index, err := wallet.getNextIndex(channel, addresstype)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	addressInfo := &BTCAddressInfo{
		Channel:      channel,
		Index:        index,
		AddressType:  addresstype,
		AddressAlias: AddressAlias,
	}
	wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList, addressInfo)

	address, addressDetails, err := wallet.loadBTCAddress(addressInfo.Channel, addressInfo.Index, addressInfo.AddressType, addressInfo.AddressAlias)
	if err != nil {
		return "", err
	}
	/*
		if wallet.addressMap[address] != nil {
			err := errors.New("address already exist")
			fmt.Println(err.Error())
			return "", err
		}

		wallet.addressMap[address] = addressDetails
	*/
	err = wallet.addToAddressList(addressDetails)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	//wallet.addressList = append(wallet.addressList, addressDetails)
	wallet.UpdateWalletInfo()
	fmt.Printf("new address = %s\n", address)
	return address, nil
}

func (wallet *BTCWallet) getNextIndex(channel uint32, addresstype uint32) (uint32, error) {

	if channel != CHANNEL_EXTERNAL && channel != CHANNEL_INTERNAL {
		err := errors.New("invalid channel, it just support 0 -- CHANNEL_EXTERNAL or 1 -- CHANNEL_INTERNAL")
		fmt.Println(err.Error())
		return 0, err
	}

	if addresstype > uint32(BTC_ADDRESS_P2TR) {
		err := errors.New("invalid address type, it just support 0 ~ 4")
		fmt.Println(err.Error())
		return 0, err

	}
	nextIndex := uint32(0)

	for _, addressInfo := range wallet.WalletInfo.AddressInfoList {
		if addressInfo.Channel == channel && addressInfo.AddressType == addresstype {
			if addressInfo.Index >= nextIndex {
				nextIndex = addressInfo.Index + 1
			}
		}
	}

	if nextIndex > 255 {
		err := errors.New("invalid index, it just support 0 ~ 255")
		fmt.Println(err.Error())
		return 0, err
	}

	return nextIndex, nil
}

func (wallet *BTCWallet) getAddressPrivateKey(address string) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	if address == "" {
		return nil, nil, errors.New("address is empty")
	}

	/*
		addressDetails := wallet.addressMap[address]
		if addressDetails == nil {
			return nil, nil, errors.New("address is invalid")
		}
	*/
	addressDetails, err := wallet.getAddressDetails(address)
	if err != nil {
		fmt.Println(err.Error())
		return nil, nil, err
	}

	if addressDetails.addresskey == nil {
		return nil, nil, errors.New("address key is invalid")
	}

	privateKey, err := addressDetails.addresskey.ECPrivKey()
	if err != nil {
		err := errors.New("invalid private key")
		fmt.Println(err.Error())
		return nil, nil, err
	}

	publicKey := privateKey.PubKey()

	fmt.Printf("private key: %x\n", privateKey.Serialize())
	fmt.Printf("public key: %x\n", publicKey.SerializeCompressed())

	return privateKey, publicKey, nil
}

var (
	// LeagacyAccountPath is the base path for BIP44 (m/44'/0'/0').
	LeagacyAccountPath = []uint32{
		44 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
	}
	// NativeSegwitAccountPath is the base path for BIP84 (m/84'/0'/0').
	NativeSegwitAccountPath = []uint32{
		84 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
	}
	// NestedSegwitAccountPath is the base path for BIP49 (m/49'/0'/0').
	NestedSegwitAccountPath = []uint32{
		49 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
	}
	// TaprootAccountPath is the base path for BIP86 (m/86'/0'/0').
	TaprootAccountPath = []uint32{
		86 + hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
	}
)

func (wallet *BTCWallet) getAccountPath(addressType uint32) ([]uint32, error) {

	switch addressType {
	case BTC_ADDRESS_P2PKH: //  Legacy P2PKH
		return LeagacyAccountPath, nil
	case BTC_ADDRESS_P2SH: //  NestedSegwit
		return NestedSegwitAccountPath, nil
	case BTC_ADDRESS_P2WKH: //  NativeSegwit
		return NativeSegwitAccountPath, nil
	case BTC_ADDRESS_P2WSH: //  P2WSH
		return NestedSegwitAccountPath, nil
	case BTC_ADDRESS_P2TR: //  Taproot
		return TaprootAccountPath, nil
	default:
		return nil, errors.New("invalid address type")
	}

}

func (wallet *BTCWallet) generatePath(addressType uint32, channel uint32, index uint32) string {
	path, err := wallet.getAccountPath(addressType)
	if err != nil {
		fmt.Println(err.Error())
		return ""
	}

	pathStr := "m"
	for _, v := range path {
		pathStr += "/" + strconv.Itoa(int(v-hdkeychain.HardenedKeyStart)) + "'"
	}

	pathStr += "/" + strconv.Itoa(int(channel)) + "/" + strconv.Itoa(int(index))

	fmt.Printf("generatePath: %s\n", pathStr)
	return pathStr
}

func (wallet *BTCWallet) getAddressKey(masterKey *hdkeychain.ExtendedKey, addressType uint32, channel uint32, index uint32) (*hdkeychain.ExtendedKey, error) {
	fmt.Println("getAddressKey...")
	accountPath, err := wallet.getAccountPath(addressType)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}
	// Get the master private key
	keyPath := append(accountPath, channel, index) // Create external account and index key
	addressKey, err := derivePath(masterKey, keyPath)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	return addressKey, nil
}
func derivePath(key *hdkeychain.ExtendedKey, path []uint32) (
	*hdkeychain.ExtendedKey, error) {

	var (
		currentKey = key
		err        error
	)
	for _, pathPart := range path {
		currentKey, err = currentKey.Derive(pathPart)
		if err != nil {
			return nil, err
		}
	}
	return currentKey, nil
}

func (wallet *BTCWallet) getMasterKey(walletName string) (*hdkeychain.ExtendedKey, error) {
	//userAccount := useraccountmanager.GetInstance()
	privatebytes, err := GetPrikey(walletName)
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}

	fmt.Printf("load data from account manager with %s /n privatebytes len = %d, privatebytes = %v\n", walletName, len(privatebytes), privatebytes)

	masterkey, err := hdkeychain.NewKeyFromString(string(privatebytes))
	if err != nil {
		// wallet account is not created
		fmt.Println(err.Error())
		return nil, err
	}

	return masterkey, nil
}

func (wallet *BTCWallet) GetDefaultAddress() (string, error) {
	defaultAddress := wallet.WalletInfo.DefaultPayAddress
	if defaultAddress == "" {
		defaultAddress = wallet.addressList[0].address
	}
	return defaultAddress, nil
}

func (wallet *BTCWallet) GetChangeAddress(paymentAddresses []string) (string, error) {
	if wallet.WalletInfo.ChangeAddress != "" {
		// Has set specific change address, should check it is valid for paymentAddress
		for _, address := range paymentAddresses {
			valid := checkValidPayToAddress(address, wallet.WalletInfo.ChangeAddress)
			if !valid {
				// If the change address is invalid, change to payment address
				return paymentAddresses[0], nil
			}
		}
		return wallet.WalletInfo.ChangeAddress, nil
	}
	return paymentAddresses[0], nil
}

func (wallet *BTCWallet) SetChangeAddress(changeAddress string) error {
	// The change address should be valid
	/*
		changeAddressDetails := wallet.addressMap[changeAddress]
		if changeAddressDetails == nil {
			err := errors.New("change address not found")
			fmt.Println(err.Error())
			return err
		}
	*/
	_, err := wallet.getAddressDetails(changeAddress)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	wallet.WalletInfo.ChangeAddress = changeAddress
	wallet.UpdateWalletInfo()
	fmt.Printf("Succeed to set change address = %s\n", changeAddress)
	return nil
}

func (wallet *BTCWallet) GetAddress(addressKey *hdkeychain.ExtendedKey, addresstype uint32) (string, error) {
	var address string
	var err error
	switch addresstype {
	case 0:
		address, err = wallet.GetLegacyAddress(addressKey)
	case 1:
		address, err = wallet.GetNestedSegwitAddress(addressKey)
	case 2:
		address, err = wallet.GetNativeSegwitAddress(addressKey)
	case 3:
		address, err = wallet.GetP2WSHAddress(addressKey)
	case 4:
		address, err = wallet.GetTaprootAddress(addressKey)
	default:
		address, err = wallet.GetLegacyAddress(addressKey)
	}
	return address, err
}

func (wallet *BTCWallet) SetDefaultAddress(defaultAddress string) error {

	_, err := wallet.getAddressDetails(defaultAddress)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	wallet.WalletInfo.DefaultPayAddress = defaultAddress
	wallet.UpdateWalletInfo()

	fmt.Printf("set default address = %s\n", defaultAddress)
	return nil
}

func (wallet *BTCWallet) GetAccountDetails() (*BTCAccountDetails, error) {
	details := &BTCAccountDetails{
		ExternalAddresses: make([]*BTCAddressSummary, 0),
		InternalAddresses: make([]*BTCAddressSummary, 0),
		AddressCount:      0,
	}

	totalChainAmount := big.NewFloat(0)
	totalMempoolAmount := big.NewFloat(0)

	for _, addressDetails := range wallet.addressList {
		addressSummy := &BTCAddressSummary{
			Address:      addressDetails.address,
			Index:        addressDetails.Index,
			Channel:      addressDetails.Channel,
			AddressType:  addressDetails.AddressType,
			AddressAlias: addressDetails.AddressAlias,
		}

		addressSummy.Path = wallet.generatePath(addressDetails.AddressType, addressDetails.Channel, addressDetails.Index)

		if addressDetails.chainAmount != nil {
			totalChainAmount = totalChainAmount.Add(totalChainAmount, addressDetails.chainAmount)
			addressSummy.Amount = addressDetails.chainAmount.String()
		} else {
			addressSummy.Amount = "0"
		}

		if addressDetails.mempoolAmount != nil {
			totalMempoolAmount = totalMempoolAmount.Add(totalMempoolAmount, addressDetails.mempoolAmount)
			addressSummy.MempoolAmount = addressDetails.mempoolAmount.String()
		} else {
			addressSummy.MempoolAmount = "0"
		}

		if addressDetails.Channel == CHANNEL_EXTERNAL {
			details.ExternalAddresses = append(details.ExternalAddresses, addressSummy)
		} else {
			details.InternalAddresses = append(details.InternalAddresses, addressSummy)
		}
		details.AddressCount++
	}
	details.TotalAmount = totalChainAmount.String()
	details.TotalMempoolAmount = totalMempoolAmount.String()
	details.ChangeAddress = wallet.WalletInfo.ChangeAddress
	details.DefaultPayAddress = wallet.WalletInfo.DefaultPayAddress

	return details, nil
}

func (wallet *BTCWallet) RefreshAccountDetails() (*BTCAccountDetails, error) {

	for _, addressDetails := range wallet.addressList {
		wallet.refreshBTCAddressDetails(addressDetails)
	}

	// get the account details
	details, err := wallet.GetAccountDetails()

	return details, err
}

func (wallet *BTCWallet) GetLegacyAddress(addressKey *hdkeychain.ExtendedKey) (string, error) {
	fmt.Println("BTCWallet: GetLegacyAddress ...")

	publicKey, err := addressKey.ECPubKey()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	publicKeyBytes := publicKey.SerializeCompressed()
	fmt.Printf("public key: %x\n", publicKeyBytes)

	pkHash := btcutil.Hash160(publicKeyBytes)
	fmt.Printf("public Hash160: %x\n", pkHash)

	scriptAddr, err := btcutil.NewAddressPubKeyHash(pkHash, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	address := scriptAddr.EncodeAddress()
	fmt.Printf("The BTC Legacy Address (P2PKH): %s\n", address)

	return address, nil
}

func (wallet *BTCWallet) GetNestedSegwitAddress(addressKey *hdkeychain.ExtendedKey) (string, error) {
	fmt.Println("BTCWallet: GetNestedSegwitAddress ...")
	/*
		publickey, err := secp256k1.ParsePubKey(wallet.walletAddr)
		if err != nil {
			fmt.Println(err.Error())
			return "", err
		}
	*/
	publicKey, err := addressKey.ECPubKey()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	publicKeyBytes := publicKey.SerializeCompressed()

	address_main, err := btcutil.NewAddressPubKey(publicKeyBytes, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	redeemScript, err := txscript.PayToAddrScript(address_main)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("redeemScript: %x\n", redeemScript)
	redeemScriptHash := sha256.Sum256(redeemScript)
	fmt.Printf("redeemScriptHash: %x\n", redeemScriptHash)

	redeemScriptHash160 := btcutil.Hash160(redeemScript)
	fmt.Printf("redeemScriptHash160: %x\n", redeemScriptHash160)

	scriptAddr, err := btcutil.NewAddressScriptHashFromHash(
		redeemScriptHash160[:], NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	address := scriptAddr.EncodeAddress()
	fmt.Printf("The BTC Nested Segwit Address (P2SH): %s\n", address)

	return address, nil
}

func (wallet *BTCWallet) GetNativeSegwitAddress(addressKey *hdkeychain.ExtendedKey) (string, error) {
	fmt.Println("BTCWallet: GetNativeSegwitAddress ...")

	publicKey, err := addressKey.ECPubKey()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	publicKeyBytes := publicKey.SerializeCompressed()
	publicKeyHash := btcutil.Hash160(publicKeyBytes)

	address_Witness, err := btcutil.NewAddressWitnessPubKeyHash(publicKeyHash, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	address := address_Witness.EncodeAddress()
	fmt.Printf("The BTC Native Segwit Address (P2WKH): %s\n", address)

	return address, nil
}

func (wallet *BTCWallet) GetP2WSHAddress(addressKey *hdkeychain.ExtendedKey) (string, error) {
	fmt.Println("BTCWallet: GetP2WSHAddress ...")

	publicKey, err := addressKey.ECPubKey()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	publicKeyBytes := publicKey.SerializeCompressed()

	fmt.Printf("The BTC Wallet publicKeyBytes: %x\n", publicKeyBytes)

	address_main, err := btcutil.NewAddressPubKey(publicKeyBytes, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	witnessScript, err := txscript.PayToAddrScript(address_main)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("The BTC Wallet witnessScript: %x\n", witnessScript)

	scriptHash := sha256.Sum256(witnessScript)
	fmt.Printf("The BTC Wallet scriptHash: %x\n", scriptHash)

	witnessScriptAddr, err := btcutil.NewAddressWitnessScriptHash(
		scriptHash[:], NetParams)

	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	address := witnessScriptAddr.EncodeAddress()
	fmt.Printf("The BTC Wallet Witness Script Address (P2WSH): %s\n", address)

	return address, nil
}

func (wallet *BTCWallet) GetTaprootAddress(addressKey *hdkeychain.ExtendedKey) (string, error) {
	fmt.Println("BTCWallet: GetTaprootAddress ...")

	pubKey, err := addressKey.ECPubKey()
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	tapKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	publicBytes := tapKey.SerializeCompressed()
	fmt.Printf("The BTC Wallet tapKey public bytes: %x\n", publicBytes)

	addrScript, err := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(tapKey),
		NetParams,
	)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	address := addrScript.EncodeAddress()
	fmt.Printf("The BTC Wallet Taproot No Script Address (P2Taproot): %s\n", address)

	return address, nil
}

func (wallet *BTCWallet) GetAddressBalance(address string) (*big.Float, *big.Float, error) {
	/*
		addressDetails := wallet.addressMap[address]
		if addressDetails == nil {
			err := errors.New("address not found")
			fmt.Println(err.Error())
			return nil, err
		}
	*/
	addressDetails, err := wallet.getAddressDetails(address)
	if err != nil {
		fmt.Println(err.Error())
		return nil, nil, err
	}

	fmt.Printf("Address balance: Chain Amount = %s, Mempool Amount = %s\n", addressDetails.chainAmount.String(), addressDetails.mempoolAmount.String())
	return addressDetails.chainAmount, addressDetails.mempoolAmount, nil
}

func (wallet *BTCWallet) Transaction(paymentAddress string, address string, amount *big.Float, fee uint32, ticker string) (string, error) {
	fmt.Printf("Will transaction %s to  %s \n", amount.String(), address)

	// Create TxOut
	pkScript, err := AddrToPkScript(address)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	fmt.Printf("Target address pkscript =  [%x]:  \n", pkScript)

	valueBTC, _ := amount.Float64()
	value, err := btcutil.NewAmount(valueBTC)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	output := &wire.TxOut{PkScript: pkScript, Value: int64(value)}
	fmt.Printf("transaction %s:  %d satoshi\n", address, value)

	// Next, create and broadcast a transaction paying to the output.
	paymentAddresses := wallet.getPaymentAddresses(paymentAddress, address)

	feeRate := btcutil.Amount(fee)
	fundTx, spentOutputs, err := wallet.CreateTransaction(paymentAddresses, []*wire.TxOut{output}, feeRate, true, ticker)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	// log the tx info
	//fmt.Printf("fund tx: %v\n", fundTx)
	LogMsgTx(fundTx)
	// jsonData, err := json.Marshal(fundTx)
	// if err != nil {
	// 	restoreutxos(spentOutputs)
	// 	fmt.Println(err.Error())
	// 	return "", err
	// }
	// fmt.Printf("fund tx json: %s\n", string(jsonData))

	// Get raw transaction for deploy to node
	rawData, err := GetRawTransaction(fundTx, true)
	if err != nil {
		restoreutxos(spentOutputs)
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("Translation raw tx: %s\n", rawData)
	// txid, err := SendTransaction(rawData)
	// if err != nil {
	// 	restoreutxos(spentOutputs)
	// 	fmt.Println(err.Error())
	// 	return "", err
	// }

	// fmt.Printf("Translation is successed with txid: %s\n", txid)
	return rawData, nil

}

func (wallet *BTCWallet) UnanchorTransaction(paymentAddress string, unanchorScript []byte, amount *big.Float, fee uint32) (string, error) {
	fmt.Printf("Will UnanchorTransaction %s \n", amount.String())

	// Create TxOut

	valueBTC, _ := amount.Float64()
	value, err := btcutil.NewAmount(valueBTC)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	output := &wire.TxOut{PkScript: unanchorScript, Value: int64(value)}
	fmt.Printf("unanchor :  %d satoshi\n", value)

	paymentAddresses := wallet.getPaymentAddresses(paymentAddress, "")

	feeRate := btcutil.Amount(fee) // No fee for unanchor tx ??
	fundTx, spentOutputs, err := wallet.CreateTransaction(paymentAddresses, []*wire.TxOut{output}, feeRate, true, "")
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	// log the tx info
	//fmt.Printf("fund tx: %v\n", fundTx)
	LogMsgTx(fundTx)
	// jsonData, err := json.Marshal(fundTx)
	// if err != nil {
	// 	restoreutxos(spentOutputs)
	// 	fmt.Println(err.Error())
	// 	return "", err
	// }
	// fmt.Printf("fund tx json: %s\n", string(jsonData))

	// Get raw transaction for deploy to node
	rawData, err := GetRawTransaction(fundTx, true)
	if err != nil {
		restoreutxos(spentOutputs)
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("Transaction raw tx: %s\n", rawData)
	// txid, err := SendTransaction(rawData)
	// if err != nil {
	// 	restoreutxos(spentOutputs)
	// 	fmt.Println(err.Error())
	// 	return "", err
	// }

	// fmt.Printf("Translation is successed with txid: %s\n", txid)
	return rawData, nil

}

func (wallet *BTCWallet) TransactionOrdx(paymentAddress string, address string, utxo string, fee uint32) (string, error) {
	fmt.Printf("Will transaction %s to  %s \n", utxo, address)

	addressDetails, err := wallet.getAddressDetails(paymentAddress)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	var value btcutil.Amount
	bFound := false

	for _, assets := range addressDetails.ordxAssets {
		utxoStrs := strings.Split(utxo, ":")
		if len(utxoStrs) != 2 {
			err := errors.New("invalid utxo")
			fmt.Println(err.Error())
			return "", err
		}
		txid := utxoStrs[0]
		vout, _ := strconv.Atoi(utxoStrs[1])
		op := &wire.OutPoint{}
		Hash, err := chainhash.NewHashFromStr(txid)
		if err != nil {
			fmt.Printf("invalid utxo - invalid txid: %v\n", err)
			return "", err
		}
		op.Hash = *Hash
		op.Index = uint32(vout)
		utxoInfo := assets.utxos[*op]
		if utxoInfo != nil {
			value = utxoInfo.value
			bFound = true
			break
		}
	}
	if bFound == false {
		err := errors.New("cannot find the utxo")
		fmt.Println(err.Error())
		return "", err
	}

	// Create TxOut
	pkScript, err := AddrToPkScript(address)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}
	fmt.Printf("Target address pkscript =  [%x]:  \n", pkScript)

	// valueBTC, _ := amount.Float64()
	// value, err := btcutil.NewAmount(valueBTC)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return "", err
	// }
	output := &wire.TxOut{PkScript: pkScript, Value: int64(value)}
	fmt.Printf("transaction %s:  %d satoshi\n", address, value)

	// Next, create and broadcast a transaction paying to the output.
	//paymentAddresses := wallet.getPaymentAddresses(paymentAddress, address)

	feeRate := btcutil.Amount(fee)
	fundTx, spentOutputs, err := wallet.CreateOrdxTransaction(paymentAddress, utxo, []*wire.TxOut{output}, feeRate, true)
	if err != nil {
		fmt.Println(err.Error())
		return "", err
	}

	// log the tx info
	fmt.Printf("fund tx: %v\n", fundTx)
	jsonData, err := json.Marshal(fundTx)
	if err != nil {
		restoreutxos(spentOutputs)
		fmt.Println(err.Error())
		return "", err
	}
	fmt.Printf("fund tx json: %s\n", string(jsonData))

	// Get raw transaction for deploy to node
	rawData, err := GetRawTransaction(fundTx, true)
	if err != nil {
		restoreutxos(spentOutputs)
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("Translation raw tx: %s\n", rawData)
	txid, err := SendTransaction(rawData)
	if err != nil {
		restoreutxos(spentOutputs)
		fmt.Println(err.Error())
		return "", err
	}

	fmt.Printf("Translation is successed with txid: %s\n", txid)
	return txid, nil

}

func restoreutxos(spentOutputs []*utxo) {
	if spentOutputs == nil {
		return
	}
	for _, utxo := range spentOutputs {
		if utxo != nil {
			utxo.isLocked = false
		}
	}
}

// func (wallet *BTCWallet) SignTx(tx *types.Transaction, s types.Signer) (*types.Transaction, error) {
// 	h := s.Hash(tx)
// 	fmt.Println("sign tx hash: %v", h)
// 	//sig, err := crypto.Sign(h[:], wallet.privateKey)
// 	userAccount := useraccountmanager.GetInstance()
// 	signed, err := userAccount.SignData(h[:], wallet.walletName)
// 	if err != nil {
// 		return nil, err
// 	}
// 	fmt.Println("signed data: %v", signed)
// 	return tx.WithSignature(s, signed)
// }

// func (wallet *BTCWallet) GetMyTxList(blockHash string) (*BTCTxList, error) {
// 	fmt.Println("GetMyTxs...s")
// 	//GetBTCTXList(blockHash)

// 	return nil, nil
// }

// BTC wallet for transaction

// utxo represents an unspent output spendable by the memWallet. The maturity
// height of the transaction is recorded in order to properly observe the
// maturity period of direct coinbase outputs.
type utxo struct {
	pkScript       []byte
	value          btcutil.Amount
	txAssets       wire.TxAssets // sats index range for the output
	keyIndex       uint32
	maturityHeight int32
	isLocked       bool
}

// isMature returns true if the target utxo is considered "mature" at the
// passed block height. Otherwise, false is returned.

func (wallet *BTCWallet) CreateTransaction(paymentAddresses []string, outputs []*wire.TxOut,
	feeRate btcutil.Amount, change bool, ticker string) (*wire.MsgTx, []*utxo, error) {
	fmt.Println("CreateTransaction ...")
	wallet.Lock()
	defer wallet.Unlock()

	tx := wire.NewMsgTx(wire.TxVersion)
	//tx := wire.NewMsgTx(2)

	// Tally up the total amount to be sent in order to perform coin
	// selection shortly below.
	var outputAmt btcutil.Amount
	for _, output := range outputs {
		outputAmt += btcutil.Amount(output.Value)
		tx.AddTxOut(output)
	}

	fmt.Printf("outputAmt = %d satoshi\n", outputAmt)

	// Attempt to fund the transaction with spendable utxos.
	if err := wallet.fundTx(paymentAddresses, tx, outputAmt, feeRate, change, ticker); err != nil {
		fmt.Println(err.Error())
		return nil, nil, err
	}

	return wallet.signTx(paymentAddresses, tx)
}

func (wallet *BTCWallet) CreateOrdxTransaction(paymentAddress string, assetsUtxo string, outputs []*wire.TxOut,
	feeRate btcutil.Amount, change bool) (*wire.MsgTx, []*utxo, error) {
	fmt.Println("CreateOrdxTransaction ...")
	wallet.Lock()
	defer wallet.Unlock()

	tx := wire.NewMsgTx(wire.TxVersion)
	//tx := wire.NewMsgTx(2)

	// Tally up the total amount to be sent in order to perform coin
	// selection shortly below.
	var outputAmt btcutil.Amount
	for _, output := range outputs {
		outputAmt += btcutil.Amount(output.Value)
		tx.AddTxOut(output)
	}

	fmt.Printf("outputAmt = %d satoshi\n", outputAmt)
	paymentAddresses := make([]string, 0)
	paymentAddresses = append(paymentAddresses, paymentAddress)

	// Attempt to fund the transaction with spendable utxos.
	if err := wallet.fundOrdxTx(paymentAddresses, assetsUtxo, tx, feeRate, change); err != nil {
		fmt.Println(err.Error())
		return nil, nil, err
	}
	fmt.Println("fundOrdxTx complete, will sign the tx ...")

	return wallet.signTx(paymentAddresses, tx)
}

func (wallet *BTCWallet) signTx(paymentAddresses []string, tx *wire.MsgTx) (*wire.MsgTx, []*utxo, error) {

	// generate prevOutFetcher
	//prevOutFetcher := txscript.NewCannedPrevOutputFetcher(pkScript, int64(utxo.value))

	prevOutFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, txIn := range tx.TxIn {
		outPoint := txIn.PreviousOutPoint
		utxo, _, err := wallet.GetUtxo(outPoint, paymentAddresses)
		if err != nil {
			fmt.Println(err.Error())
			return nil, nil, err
		}
		prevOutFetcher.AddPrevOut(
			outPoint, &wire.TxOut{Value: int64(utxo.value), Assets: utxo.txAssets, PkScript: utxo.pkScript},
		)
	}

	hashCache := txscript.NewTxSigHashes(tx, prevOutFetcher)

	// Populate all the selected inputs with valid sigScript for spending.
	// Along the way record all outputs being spent in order to avoid a
	// potential double spend.
	spentOutputs := make([]*utxo, 0, len(tx.TxIn))

	for i, txIn := range tx.TxIn {
		outPoint := txIn.PreviousOutPoint
		//utxo := wallet.utxos[outPoint]
		utxo, address, err := wallet.GetUtxo(outPoint, paymentAddresses)
		if err != nil {
			fmt.Println(err.Error())
			return nil, nil, err
		}
		fmt.Printf("vin Index  %d, outPoint = %+v, utxo = %v, \n", i, outPoint, utxo)

		//privateKey, _, err := wallet.getbtcPrivateKey(addressKey)
		privateKey, _, err := wallet.getAddressPrivateKey(address)
		if err != nil {
			fmt.Println(err.Error())
			return nil, nil, err
		}

		pkScript := utxo.pkScript
		fmt.Printf("The index %d pkScript is: %s\n", i, hex.EncodeToString(pkScript))

		//prevOutFetcher := txscript.NewCannedPrevOutputFetcher(pkScript, int64(utxo.value))

		//hashCache := txscript.NewTxSigHashes(tx, prevOutFetcher)

		switch {
		// If this is a p2sh output, who's script hash pre-image is a
		// witness program, then we'll need to use a modified signing
		// function which generates both the sigScript, and the witness
		// script.
		case txscript.IsPayToScriptHash(pkScript):
			fmt.Println("The pkScript IsPayToScriptHash: P2SH")

			err := spendNestedWitnessPubKeyHash(
				txIn, pkScript, int64(utxo.value),
				NetParams, privateKey, tx, hashCache, i,
				prevOutFetcher)
			if err != nil {
				return nil, nil, err
			}

		case txscript.IsPayToWitnessPubKeyHash(pkScript):
			fmt.Println("The pkScript IsPayToWitnessPubKeyHash: P2WKH")

			err := spendWitnessKeyHash(
				txIn, pkScript, int64(utxo.value), utxo.txAssets,
				NetParams, privateKey, tx, hashCache, i,
				prevOutFetcher)
			if err != nil {
				fmt.Println("spendWitnessKeyHash failed")
				return nil, nil, err
			}

		case txscript.IsPayToWitnessScriptHash(pkScript):
			fmt.Println("The pkScript IsPayToWitnessScriptHash:P2WSH")
			err := spendWitnessScriptHash(
				txIn, pkScript, int64(utxo.value), utxo.txAssets,
				NetParams, privateKey, tx, hashCache, i,
				prevOutFetcher)
			if err != nil {
				fmt.Println("spendWitnessKeyHash failed")
				return nil, nil, err
			}

		case txscript.IsPayToTaproot(pkScript):
			fmt.Println("The pkScript IsTaprootKeyHash: Taproot")
			err := spendTaprootKey(
				txIn, pkScript, int64(utxo.value), utxo.txAssets,
				NetParams, privateKey, tx, hashCache, i,
				prevOutFetcher)
			if err != nil {
				return nil, nil, err
			}

		default:
			fmt.Println("The pkScript is Default: IsPayToPublicKeyHash: P2PKH")

			sigScript, err := txscript.SignatureScript(tx, i, utxo.pkScript,
				txscript.SigHashAll, privateKey, true)
			if err != nil {
				fmt.Println(err.Error())
				return nil, nil, err
			}
			fmt.Printf("sigScript: %+v\n", sigScript)
			txIn.SignatureScript = sigScript

		}

		// Verify the tx to ensure the output parameters are correct.
		prevOut := prevOutFetcher.FetchPrevOutput(
			txIn.PreviousOutPoint,
		)

		// These are meant to fail, so as soon as the first
		// input fails the transaction has failed. (some of the
		// test txns have good inputs, too..
		vm, err := txscript.NewEngine(prevOut.PkScript, tx, i,
			txscript.StandardVerifyFlags, nil, hashCache, prevOut.Value, prevOut.Assets, prevOutFetcher)
		if err != nil {
			fmt.Printf("NewEngine failed: %v\n", err)
			return nil, nil, err
		}

		err = vm.Execute()
		if err != nil {
			fmt.Printf("NewEngine verify failed: %v\n", err)
			return nil, nil, err
		}
		fmt.Printf("index %d txin is verfied.\n", i)

		spentOutputs = append(spentOutputs, utxo)
	}

	// As these outputs are now being spent by this newly created
	// transaction, mark the outputs are "locked". This action ensures
	// these outputs won't be double spent by any subsequent transactions.
	// These locked outputs can be freed via a call to UnlockOutputs.
	for _, utxo := range spentOutputs {
		utxo.isLocked = true
	}

	return tx, spentOutputs, nil
}

// fundTx attempts to fund a transaction sending amt bitcoin. The coins are
// selected such that the final amount spent pays enough fees as dictated by the
// passed fee rate. The passed fee rate should be expressed in
// satoshis-per-byte. The transaction being funded can optionally include a
// change output indicated by the change boolean.
//
// NOTE: The memWallet's mutex must be held when this function is called.
func (wallet *BTCWallet) fundTx(paymentAddresses []string, tx *wire.MsgTx, amt btcutil.Amount,
	feeRate btcutil.Amount, change bool, ticker string) error {
	fmt.Printf("fundTx: amt = %d, feeRate = %d, \n", amt, feeRate)

	const (
		// spendSize is the largest number of bytes of a sigScript
		// which spends a p2pkh output: OP_DATA_73 <sig> OP_DATA_33 <pubkey>
		spendSize = 1 + 73 + 1 + 33
	)

	var (
		amtSelected btcutil.Amount
		txSize      int
	)

	var utxos map[wire.OutPoint]*utxo
	var err error
	// if ticker != "" {
	// 	utxos, err = wallet.getUtxosForInscription(paymentAddresses, ticker)
	// 	if err != nil {
	// 		fmt.Println(err.Error())
	// 		return err
	// 	}

	// } else {
	utxos, err = wallet.getUtxos(paymentAddresses)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	//}
	inputTxAssets := wire.TxAssets{}

	for outPoint, utxo := range utxos {
		// Skip any outputs that are still currently immature or are
		// currently locked.
		if utxo.isLocked {
			continue
		}

		fmt.Printf("Add to TX in : outPoint = %+v, utxo = %+v, \n", outPoint, utxo)

		amtSelected += utxo.value

		// Add the selected output to the transaction, updating the
		// current tx size while accounting for the size of the future
		// sigScript.
		tx.AddTxIn(wire.NewTxIn(&outPoint, nil, nil))
		txSize = tx.SerializeSize() + spendSize*len(tx.TxIn)

		logTxAssets("fundtx txin", utxo.txAssets)

		//InputTxRanges = wire.TxRangesAppend(InputTxRanges, utxo.satsRanges)
		inputTxAssets.Merge(utxo.txAssets)

		// Calculate the fee required for the txn at this point
		// observing the specified fee rate. If we don't have enough
		// coins from he current amount selected to pay the fee, then
		// continue to grab more coins.
		reqFee := btcutil.Amount(txSize * int(feeRate))

		fmt.Printf("amtSelected = %d,  reqFee = %d, amt = %d, \n", amtSelected, reqFee, amt)

		if amtSelected-reqFee < amt {
			fmt.Println(" It isnot enough now, continue next utxo ")
			continue
		}

		// If we have any change left over and we should create a change
		// output, then add an additional output to the transaction
		// reserved for it.
		changeVal := amtSelected - amt - reqFee

		fmt.Printf(" It is enough now, calc changer is %d \n", changeVal)

		if changeVal > 0 && change {
			changeAddress, err := wallet.GetChangeAddress(paymentAddresses)
			if err != nil {
				return err
			}
			fmt.Printf(" change address is %s \n", changeAddress)

			pkScript, err := AddrToPkScript(changeAddress)
			if err != nil {
				return err
			}
			changeOutput := &wire.TxOut{
				Value:    int64(changeVal),
				PkScript: pkScript,
			}
			// Add changer to tx out
			fmt.Println("Add changer to tx out ")
			tx.AddTxOut(changeOutput)
		}

		// Fill output TxRanges
		logTxAssets("fundtx all txin ranges", inputTxAssets)

		// rangeOffset := int64(0)
		// for _, txoutItem := range tx.TxOut {
		// 	itemRanges, err := logTxAssets.Pickup(rangeOffset, txoutItem.Value)
		// 	if err != nil {
		// 		fmt.Println("Set output sats ranges failed: error: " + err.Error())
		// 		return err
		// 	}
		// 	txoutItem.SatsRanges = itemRanges
		// 	rangeOffset += int64(txoutItem.Value)
		// }

		return nil
	}

	reqFee := btcutil.Amount(txSize * int(feeRate))
	fmt.Printf("amtSelected = %d,  reqFee = %d, amt = %d, \n", amtSelected, reqFee, amt)
	if amt.ToBTC() == AMT_ALL {
		// It's used to ACCOUNT collection, it should be only 1 output
		if len(tx.TxOut) == 1 {
			// All amt is selected and add to output
			tx.TxOut[0].Value = int64(amtSelected) - int64(reqFee)
		}

		// Fill output TxRanges
		// rangeOffset := int64(0)
		// for _, txoutItem := range tx.TxOut {
		// 	itemRanges, err := InputTxRanges.Pickup(rangeOffset, txoutItem.Value)
		// 	if err != nil {
		// 		fmt.Println("Set output sats ranges failed: error: " + err.Error())
		// 		return err
		// 	}
		// 	txoutItem.SatsRanges = itemRanges
		// 	rangeOffset += int64(txoutItem.Value)
		// }

		return nil
	}

	// If we've reached this point, then coin selection failed due to an
	// insufficient amount of coins.
	err = fmt.Errorf("not enough funds for coin selection")
	fmt.Println(err.Error())
	return err
}

// fundTx attempts to fund a transaction sending amt bitcoin. The coins are
// selected such that the final amount spent pays enough fees as dictated by the
// passed fee rate. The passed fee rate should be expressed in
// satoshis-per-byte. The transaction being funded can optionally include a
// change output indicated by the change boolean.
//
// NOTE: The memWallet's mutex must be held when this function is called.
func (wallet *BTCWallet) fundOrdxTx(paymentAddresses []string, ordxUtxo string, tx *wire.MsgTx,
	feeRate btcutil.Amount, change bool) error {
	fmt.Printf("fundOrdxTx for transfer utxo: %s, \n", ordxUtxo)

	const (
		// spendSize is the largest number of bytes of a sigScript
		// which spends a p2pkh output: OP_DATA_73 <sig> OP_DATA_33 <pubkey>
		spendSize = 1 + 73 + 1 + 33
	)

	var (
		amtSelected btcutil.Amount
		txSize      int
	)

	var err error

	// The first address is ordx holder address
	paymentAddress := paymentAddresses[0]

	// Get utxo for pay gas
	addressDetails, err := wallet.getAddressDetails(paymentAddress)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	utxos := addressDetails.utxos

	// Add ordx assets input
	ordxOutPoint, err := wallet.getOrdxAssets(paymentAddress, ordxUtxo) // getOrdxAssets
	tx.AddTxIn(wire.NewTxIn(ordxOutPoint, nil, nil))

	// Calc the Gas fee for transfer ordx assets
	txSize = tx.SerializeSize() + spendSize*len(tx.TxIn)
	amt := btcutil.Amount(txSize * int(feeRate))

	fmt.Printf("need to pay gas = %d, \n", amt)

	for outPoint, utxo := range utxos {
		// Skip any outputs that are still currently immature or are
		// currently locked.
		if utxo.isLocked {
			continue
		}

		fmt.Printf("Add to TX in : outPoint = %+v, utxo = %+v, \n", outPoint, utxo)

		amtSelected += utxo.value

		// Add the selected output to the transaction, updating the
		// current tx size while accounting for the size of the future
		// sigScript.
		tx.AddTxIn(wire.NewTxIn(&outPoint, nil, nil))
		txSize = tx.SerializeSize() + spendSize*len(tx.TxIn)

		// Calculate the fee required for the txn at this point
		// observing the specified fee rate. If we don't have enough
		// coins from he current amount selected to pay the fee, then
		// continue to grab more coins.
		reqFee := btcutil.Amount(txSize * int(feeRate))

		fmt.Printf("amtSelected = %d,  reqFee = %d, amt = %d, \n", amtSelected, reqFee, amt)

		if amtSelected-reqFee < amt {
			fmt.Println(" It isnot enough now, continue next utxo ")
			continue
		}

		// If we have any change left over and we should create a change
		// output, then add an additional output to the transaction
		// reserved for it.
		changeVal := amtSelected - amt - reqFee

		fmt.Printf(" It is enough now, calc changer is %d \n", changeVal)

		if changeVal > 0 && change {
			changeAddress, err := wallet.GetChangeAddress(paymentAddresses)
			if err != nil {
				return err
			}
			fmt.Printf(" change address is %s \n", changeAddress)

			pkScript, err := AddrToPkScript(changeAddress)
			if err != nil {
				return err
			}
			changeOutput := &wire.TxOut{
				Value:    int64(changeVal),
				PkScript: pkScript,
			}
			// Add changer to tx out
			fmt.Println("Add changer to tx out ")
			tx.AddTxOut(changeOutput)
		}

		return nil
	}

	reqFee := btcutil.Amount(txSize * int(feeRate))
	fmt.Printf("amtSelected = %d,  reqFee = %d, amt = %d, \n", amtSelected, reqFee, amt)
	if amt.ToBTC() == AMT_ALL {
		// It's used to ACCOUNT collection, it should be only 1 output
		if len(tx.TxOut) == 1 {
			// All amt is selected and add to output
			tx.TxOut[0].Value = int64(amtSelected) - int64(reqFee)
		}
		return nil
	}

	// If we've reached this point, then coin selection failed due to an
	// insufficient amount of coins.
	err = fmt.Errorf("not enough funds for coin selection")
	fmt.Println(err.Error())
	return err
}

// spendWitnessKeyHash generates, and sets a valid witness for spending the
// passed pkScript with the specified input amount. The input amount *must*
// correspond to the output value of the previous pkScript, or else verification
// will fail since the new sighash digest algorithm defined in BIP0143 includes
// the input value in the sighash.
func spendWitnessKeyHash(txIn *wire.TxIn, pkScript []byte,
	inputValue int64, txAssets wire.TxAssets, chainParams *chaincfg.Params, privateKey *btcec.PrivateKey,
	tx *wire.MsgTx, hashCache *txscript.TxSigHashes, idx int,
	inputFetcher txscript.PrevOutputFetcher) error {

	// First obtain the key pair associated with this p2wkh address.
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript,
		chainParams)
	if err != nil {
		return err
	}

	fmt.Printf("spendWitnessKeyHash: addrs: %v\n", addrs[0].EncodeAddress())
	/*
		privKey, compressed, err := secrets.GetKey(addrs[0])
		if err != nil {
			return err
		}
	*/

	pubKey := privateKey.PubKey()
	compressed := true

	// Once we have the key pair, generate a p2wkh address type, respecting
	// the compression type of the generated key.
	var pubKeyHash []byte
	if compressed {
		pubKeyHash = btcutil.Hash160(pubKey.SerializeCompressed())
	} else {
		pubKeyHash = btcutil.Hash160(pubKey.SerializeUncompressed())
	}
	p2wkhAddr, err := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, chainParams)
	if err != nil {
		return err
	}

	// With the concrete address type, we can now generate the
	// corresponding witness program to be used to generate a valid witness
	// which will allow us to spend this output.

	witnessProgram, err := txscript.PayToAddrScript(p2wkhAddr)
	if err != nil {
		return err
	}

	fmt.Printf("witnessProgram: [%x]\n", witnessProgram)
	witnessScript, err := txscript.WitnessSignature(tx, hashCache, idx,
		inputValue, txAssets, witnessProgram, txscript.SigHashAll, privateKey, true)
	if err != nil {
		return err
	}

	txIn.Witness = witnessScript

	// Check sign
	/*
		engine, err := txscript.NewEngine(pkScript, tx, idx, txscript.StandardVerifyFlags, nil, hashCache, inputValue, inputFetcher)
		if err != nil {
			fmt.Println(err.Error())
			return err
		}

		err = engine.Execute()
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
	*/
	fmt.Println("spendWitnessKeyHash: done")
	return nil
}

// spendWitnessScriptHash generates, and sets a valid witness for spending the
// passed pkScript with the specified input amount. The input amount *must*
// correspond to the output value of the previous pkScript, or else verification
// will fail since the new sighash digest algorithm defined in BIP0143 includes
// the input value in the sighash.
func spendWitnessScriptHash(txIn *wire.TxIn, pkScript []byte,
	inputValue int64, txAssets wire.TxAssets, chainParams *chaincfg.Params, privateKey *btcec.PrivateKey,
	tx *wire.MsgTx, hashCache *txscript.TxSigHashes, idx int,
	inputFetcher txscript.PrevOutputFetcher) error {

	fmt.Printf("spendWitnessScriptHash: pkScript: %x\n", pkScript)

	// First obtain the key pair associated with this p2wkh address.
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript,
		chainParams)
	if err != nil {
		return err
	}

	fmt.Printf("spendWitnessKeyHash: addrs: %v\n", addrs[0].EncodeAddress())
	/*
		privKey, compressed, err := secrets.GetKey(addrs[0])
		if err != nil {
			return err
		}
	*/

	pubKey := privateKey.PubKey()
	compressed := true

	// Once we have the key pair, generate a p2wkh address type, respecting
	// the compression type of the generated key.
	var publicKeyBytes []byte
	if compressed {
		publicKeyBytes = pubKey.SerializeCompressed()
	} else {
		publicKeyBytes = pubKey.SerializeUncompressed()
	}

	address_main, err := btcutil.NewAddressPubKey(publicKeyBytes, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	witnessScript, err := txscript.PayToAddrScript(address_main)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	fmt.Printf("The BTC Wallet witnessScript: %x\n", witnessScript)

	scriptHash := sha256.Sum256(witnessScript)
	fmt.Printf("The BTC Wallet scriptHash: %x\n", scriptHash)

	/*
		witnessScriptAddr, err := btcutil.NewAddressWitnessScriptHash(
			scriptHash[:], NetParams)

		if err != nil {
			fmt.Println(err.Error())
			return err
		}

		// With the concrete address type, we can now generate the
		// corresponding witness program to be used to generate a valid witness
		// which will allow us to spend this output.

		witnessProgram, err := txscript.PayToAddrScript(witnessScriptAddr)
	*/
	//witnessProgram, err := txscript.PayToAddrScript(pkScript)
	witnessProgram := scriptHash[:]
	if err != nil {
		return err
	}
	fmt.Printf("witnessProgram: [%x]\n", witnessProgram)
	/*
		witness, err := txscript.WitnessSignature(tx, hashCache, idx,
			inputValue, pkScript, txscript.SigHashAll, privateKey, true)
		if err != nil {
			return err
		}
	*/

	sig, err := txscript.RawTxInWitnessSignature(tx, hashCache, idx, inputValue, txAssets, witnessScript,
		txscript.SigHashAll, privateKey)
	if err != nil {
		return err
	}

	txIn.Witness = wire.TxWitness{sig, witnessScript}

	// Check sign
	/*
		engine, err := txscript.NewEngine(pkScript, tx, idx, txscript.StandardVerifyFlags, nil, hashCache, inputValue, inputFetcher)
		if err != nil {
			fmt.Println(err.Error())
			return err
		}

		err = engine.Execute()
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
	*/
	fmt.Println("spendWitnessScriptHash: done")
	return nil
}

// spendTaprootKey generates, and sets a valid witness for spending the passed
// pkScript with the specified input amount. The input amount *must*
// correspond to the output value of the previous pkScript, or else verification
// will fail since the new sighash digest algorithm defined in BIP0341 includes
// the input value in the sighash.
func spendTaprootKey(txIn *wire.TxIn, pkScript []byte,
	inputValue int64, txAssets wire.TxAssets, chainParams *chaincfg.Params, privateKey *btcec.PrivateKey,
	tx *wire.MsgTx, hashCache *txscript.TxSigHashes, idx int,
	inputFetcher txscript.PrevOutputFetcher) error {

	// First obtain the key pair associated with this p2tr address. If the
	// pkScript is incorrect or derived from a different internal key or
	// with a script root, we simply won't find a corresponding private key
	// here.

	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, chainParams)
	if err != nil {
		return err
	}

	fmt.Printf("Taproot pkScript: %x\n", pkScript)
	fmt.Printf("Taproot Address: %s\n", addrs[0].EncodeAddress())

	//fmt.Println("Taproot Sign private key : %x", privateKey.Key.String())
	//fmt.Println("Taproot Sign public key : %x", privateKey.PubKey().SerializeCompressed())
	//fmt.Println("Taproot Address: %s", addrs[0].EncodeAddress())

	witnessProgram, err := txscript.PayToAddrScript(addrs[0])
	if err != nil {
		return err
	}
	fmt.Printf("The BTC Wallet Taproot witnessProgram: %x\n", witnessProgram)

	/*
		witVersion, witProgram, err := txscript.ExtractWitnessProgramInfo(
			witnessProgram,
		)
		if err != nil {
			return err
		}
		fmt.Println("The BTC Wallet Taproot witVersion: %x", witVersion)
		fmt.Println("The BTC Wallet Taproot witProgram: %x", witProgram)
	*/

	// We can now generate a valid witness which will allow us to spend this
	// output.
	witnessScript, err := txscript.TaprootWitnessSignature(
		tx, hashCache, idx, inputValue, txAssets, witnessProgram,
		txscript.SigHashDefault, privateKey,
	)
	if err != nil {
		return err
	}

	//witnessScript = append(witnessScript, witnessProgram)

	txIn.Witness = witnessScript

	fmt.Printf("witnessScript: %x\n", witnessScript)

	// Check sign
	/*
		engine, err := txscript.NewEngine(pkScript, tx, idx, txscript.StandardVerifyFlags, nil, hashCache, inputValue, inputFetcher)
		if err != nil {
			fmt.Println(err.Error())
			return err
		}

		err = engine.Execute()
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
	*/

	return nil
}

// spendNestedWitnessPubKey generates both a sigScript, and valid witness for
// spending the passed pkScript with the specified input amount. The generated
// sigScript is the version 0 p2wkh witness program corresponding to the queried
// key. The witness stack is identical to that of one which spends a regular
// p2wkh output. The input amount *must* correspond to the output value of the
// previous pkScript, or else verification will fail since the new sighash
// digest algorithm defined in BIP0143 includes the input value in the sighash.
func spendNestedWitnessPubKeyHash(txIn *wire.TxIn, pkScript []byte,
	inputValue int64, chainParams *chaincfg.Params, privateKey *btcec.PrivateKey,
	tx *wire.MsgTx, hashCache *txscript.TxSigHashes, idx int,
	inputFetcher txscript.PrevOutputFetcher) error {

	_, addrs, _, err := txscript.ExtractPkScriptAddrs(pkScript, chainParams)
	if err != nil {
		return err
	}

	fmt.Printf("Transfer address: %s\n", addrs[0].EncodeAddress())
	fmt.Printf("pkScript: %x\n", pkScript)

	pubKey := privateKey.PubKey()

	// Once we have the key pair, generate a p2wkh address type, respecting
	// the compression type of the generated key.
	publicKeyBytes := pubKey.SerializeCompressed()

	address_main, err := btcutil.NewAddressPubKey(publicKeyBytes, NetParams)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	redeemScript, err := txscript.PayToAddrScript(address_main)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	fmt.Printf("redeemScript: %x\n", redeemScript)

	// Generate a signature.
	sig1WithHT, err := txscript.RawTxInSignature(tx, idx, redeemScript,
		txscript.SigHashAll, privateKey)
	if err != nil {
		return err
	}
	fmt.Printf("Signature 1: %x\n", sig1WithHT)

	// Generate the final signature script and update the transaction with it.
	sigScript, err := txscript.NewScriptBuilder().AddData(sig1WithHT).AddData(redeemScript).Script()
	if err != nil {
		return err
	}
	fmt.Printf("sigScript: %x\n", sigScript)

	txIn.SignatureScript = sigScript

	// Check sign
	/*
		engine, err := txscript.NewEngine(pkScript, tx, idx, txscript.StandardVerifyFlags, nil, hashCache, inputValue, inputFetcher)
		if err != nil {
			fmt.Println(err.Error())
			return err
		}

		err = engine.Execute()
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
	*/

	return nil
}

// func AddrToPkScript(address string) ([]byte, error) {
// 	a, err := btcutil.DecodeAddress(address, NetParams)
// 	if err != nil {
// 		fmt.Println(err.Error())
// 		return nil, err
// 	}

// 	pkScript, err := txscript.PayToAddrScript(a)
// 	if err != nil {
// 		fmt.Println(err.Error())
// 		return nil, err
// 	}

// 	return pkScript, nil
// }

func checkValidPayToAddress(paymentAddress string, receivingAddress string) bool {
	if receivingAddress == "" {
		// No receiving address, all address is valid
		return true
	}
	paymentAddressType, err := getAddressType(paymentAddress)
	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	receivingAddressType, err := getAddressType(receivingAddress)
	if err != nil {
		fmt.Println(err.Error())
		return false
	}

	if paymentAddressType == BTC_ADDRESS_P2TR && receivingAddressType != BTC_ADDRESS_P2TR {
		// If the payment address is P2TR, then the receiving address should be P2TR, if not return false
		return false
	}

	return true
}

func GetScriptType(pkScript []byte) (uint32, error) {

	switch {
	// If this is a p2sh output, who's script hash pre-image is a
	// witness program, then we'll need to use a modified signing
	// function which generates both the sigScript, and the witness
	// script.
	case txscript.IsPayToPubKeyHash(pkScript):
		fmt.Println("The pkScript is Default: IsPayToPublicKeyHash: P2PKH")
		return BTC_ADDRESS_P2PKH, nil
	case txscript.IsPayToScriptHash(pkScript):
		fmt.Println("The pkScript IsPayToScriptHash: P2SH")
		return BTC_ADDRESS_P2SH, nil

	case txscript.IsPayToWitnessScriptHash(pkScript):
		fmt.Println("The pkScript IsPayToWitnessScriptHash:P2WSH")
		return BTC_ADDRESS_P2WSH, nil

	case txscript.IsPayToWitnessPubKeyHash(pkScript):
		fmt.Println("The pkScript IsPayToWitnessPubKeyHash: P2WKH")
		return BTC_ADDRESS_P2WKH, nil

	case txscript.IsPayToTaproot(pkScript):
		fmt.Println("The pkScript IsTaprootKeyHash: Taproot")
		return BTC_ADDRESS_P2TR, nil

	default:
		fmt.Println("The pkScript unknown type")
		err := errors.New("unknown address type")
		return BTC_ADDRESS_UNKNOWN, err

	}

}

func getAddressType(address string) (uint32, error) {
	pkScript, err := AddrToPkScript(address)
	if err != nil {
		fmt.Println(err.Error())
		return BTC_ADDRESS_UNKNOWN, err
	}

	fmt.Printf("The address pkscript =  [%x]:  \n", pkScript)

	addressType, err := GetScriptType(pkScript)
	if err != nil {
		fmt.Println(err.Error())
		return BTC_ADDRESS_UNKNOWN, err
	}

	return addressType, nil
}

func (wallet *BTCWallet) GetUtxo(outPoint wire.OutPoint, paymentAddresses []string) (*utxo, string, error) {
	if paymentAddresses == nil {
		err := errors.New("utxo not found")
		fmt.Println(err.Error())
		return nil, "", err
	}

	// Search utxo from all addresses
	for _, address := range paymentAddresses {
		/*
			addressDetails := wallet.addressMap[address]
			if addressDetails == nil {
				// The address is invalid
				continue
			}*/
		addressDetails, err := wallet.getAddressDetails(address)
		if err != nil {
			fmt.Println(err.Error())
			return nil, "", err
		}

		utxo := addressDetails.utxos[outPoint]
		if utxo != nil {
			return utxo, address, nil
		}

		// The utxo is an ordx assets
		ordxAssets := addressDetails.ordxAssets
		for _, ordxAsset := range ordxAssets {
			utxo := ordxAsset.utxos[outPoint]
			if utxo != nil {
				return utxo, address, nil
			}
		}
	}

	err := errors.New("utxo not found")
	fmt.Println(err.Error())
	return nil, "", err

}

func (wallet *BTCWallet) getUtxos(paymentAddresses []string) (map[wire.OutPoint]*utxo, error) {
	// Search utxo from all addresses
	utxos := make(map[wire.OutPoint]*utxo)
	for _, address := range paymentAddresses {
		/*
			addressDetails := wallet.addressMap[address]
			if addressDetails == nil {
				err := errors.New("address not found")
				fmt.Println(err.Error())
				continue
			}
		*/
		addressDetails, err := wallet.getAddressDetails(address)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}

		if addressDetails.utxos == nil {
			continue
		}
		for outPoint, utxo := range addressDetails.utxos {
			utxos[outPoint] = utxo
		}
	}

	return utxos, nil
}

func (wallet *BTCWallet) getPaymentAddresses(paymentAddress string, receiveAddress string) []string {
	fmt.Printf("getPaymentAddresses: %s, %s\n", paymentAddress, receiveAddress)

	paymentAddresses := make([]string, 0)

	if paymentAddress != "" {
		// Add the specific payment address
		paymentAddresses = append(paymentAddresses, paymentAddress)
		return paymentAddresses
	}

	for _, addressDetails := range wallet.addressList {
		if addressDetails.address == receiveAddress {
			// The receiving address cannot be used as a payment address
			continue
		}
		if checkValidPayToAddress(addressDetails.address, receiveAddress) {
			// The address is valid for pay to receive address
			paymentAddresses = append(paymentAddresses, addressDetails.address)
		}
	}

	fmt.Printf("paymentAddresses: %v\n", paymentAddresses)
	return paymentAddresses
}

func (wallet *BTCWallet) loadWalletInfo() error {

	walletInfo, err := GetWalletInfo(wallet.walletName)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	wallet.WalletInfo = walletInfo

	return nil
}

func (wallet *BTCWallet) UpdateWalletInfo() error {
	fmt.Println("UpdateWalletInfo...")
	err := SetWalletInfo(wallet.walletName, wallet.WalletInfo)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	// userAccount := useraccountmanager.GetInstance()
	// userID_key, _ := userAccount.GetUserAccountID(useraccountmanager.USERACCOUNT_STORAGE)
	// userID := key.TranslateKeyProtoBufToString(userID_key)
	// key := fmt.Sprintf(BTC_WalletInfo_Templete, sdk.GetInstance().GetAppName(), userID, wallet.walletName)

	// data, err := proto.Marshal(wallet.WalletInfo)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return err
	// }
	// encrptedData, err := userAccount.EncryptAES(data, useraccountmanager.USERACCOUNT_STORAGE)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return err
	// }

	// fmt.Println("UpdateBlock with %s", key)

	// err = sdkutils.SaveToDKVS(key, encrptedData, defines.DEFAULT_TTL)
	// if err != nil {
	// 	fmt.Println(err.Error())
	// 	return err
	// }

	//fmt.Println("UpdateBlock succeed to %s", key)
	return nil
}

func (wallet *BTCWallet) AccountCollect(address string, feeRate uint32) (string, error) {
	amountTx := big.NewFloat(AMT_ALL)

	tx, err := wallet.Transaction("", address, amountTx, feeRate, "")
	if err == nil {
		fmt.Printf("Account Collect succeed, the tx: %s\n", tx)
		return tx, nil
	}

	return "", err
}

func (wallet *BTCWallet) getAddressDetails(address string) (*BTCAddressDetails, error) {

	for _, addressDetails := range wallet.addressList {
		if addressDetails.address == address {
			return addressDetails, nil
		}
	}

	err := errors.New("address not found")
	fmt.Println(err.Error())
	return nil, err

}

func (wallet *BTCWallet) addToAddressList(addressDetails *BTCAddressDetails) error {

	for _, details := range wallet.addressList {
		if details.address == addressDetails.address {
			// The address is exist in list
			err := errors.New("address is existed")
			fmt.Println(err.Error())
			return err
		}
	}

	wallet.addressList = append(wallet.addressList, addressDetails)

	return nil
}

func (wallet *BTCWallet) removeFromAddressList(address string) error {

	for index, addressDetails := range wallet.addressList {
		if addressDetails.address == address {
			// Delete the address
			if index == 0 {
				// First address
				wallet.addressList = wallet.addressList[1:]
			} else if index == len(wallet.addressList)-1 {
				// Last address
				wallet.addressList = wallet.addressList[:index]
			} else {
				// Middle address
				wallet.addressList = append(wallet.addressList[:index], wallet.addressList[index+1:]...)
			}
			return nil
		}
	}

	err := errors.New("address not found")
	fmt.Println(err.Error())
	return err
}

func (wallet *BTCWallet) DelAddress(address string) error {

	for index, addressDetails := range wallet.addressList {
		if addressDetails.address == address {
			// Delete the address
			if index == 0 {
				// First address
				wallet.addressList = wallet.addressList[1:]
			} else if index == len(wallet.addressList)-1 {
				// Last address
				wallet.addressList = wallet.addressList[:index]
			} else {
				// Middle address
				wallet.addressList = append(wallet.addressList[:index], wallet.addressList[index+1:]...)
			}

			// Delete from wallet info
			err := wallet.delAddressInfo(addressDetails.Channel, addressDetails.Index, addressDetails.AddressType)
			if err != nil {
				fmt.Println(err.Error())
				return err
			}

			return nil
		}
	}

	err := errors.New("address not found")
	fmt.Println(err.Error())
	return err
}

func (wallet *BTCWallet) delAddressInfo(channel uint32, indexAddress uint32, addressType uint32) error {

	for index, addressInfo := range wallet.WalletInfo.AddressInfoList {
		if addressInfo.Channel == channel && addressInfo.Index == indexAddress && addressInfo.AddressType == uint32(addressType) {
			// Delete the address
			if index == 0 {
				// First address
				wallet.WalletInfo.AddressInfoList = wallet.WalletInfo.AddressInfoList[1:]
			} else if index == len(wallet.addressList)-1 {
				// Last address
				wallet.WalletInfo.AddressInfoList = wallet.WalletInfo.AddressInfoList[:index]
			} else {
				// Middle address
				wallet.WalletInfo.AddressInfoList = append(wallet.WalletInfo.AddressInfoList[:index], wallet.WalletInfo.AddressInfoList[index+1:]...)
			}
			wallet.UpdateWalletInfo()
			return nil
		}
	}

	err := errors.New("address not found")
	fmt.Println(err.Error())
	return err
}

func (wallet *BTCWallet) SetAddressAlias(address string, alias string) error {

	details, err := wallet.getAddressDetails(address)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	for _, addressInfo := range wallet.WalletInfo.AddressInfoList {
		if addressInfo.Channel == details.Channel && addressInfo.Index == details.Index && addressInfo.AddressType == details.AddressType {
			// The address is specific address
			details.AddressAlias = alias

			// update the wallet info
			addressInfo.AddressAlias = alias
			wallet.UpdateWalletInfo()
			return nil
		}
	}

	err = errors.New("address not found")
	fmt.Println(err.Error())
	return err
}

// func DeployOrdx() {
// 	inscriptionBuilder := txscript.NewScriptBuilder().
// 		AddData(schnorr.SerializePubKey(privateKey.PubKey())).
// 		AddOp(txscript.OP_CHECKSIG).
// 		AddOp(txscript.OP_FALSE).
// 		AddOp(txscript.OP_IF).
// 		AddData([]byte("ord")).
// 		AddOp(txscript.OP_DATA_1).
// 		AddOp(txscript.OP_DATA_1).
// 		AddData([]byte(inscriptionRequest.InscriptionDataList[indexOfInscriptionDataList].ContentType)).
// 		AddOp(txscript.OP_0)
// }

func (wallet *BTCWallet) GetAddressOrdxAssets(adddress string) ([]*OrdxAssetsInfo, error) {
	details, err := wallet.getAddressDetails(adddress)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	if details.ordxAssets != nil {
		assetsInfoList := make([]*OrdxAssetsInfo, 0)
		for _, assets := range details.ordxAssets {
			assetsInfo := &OrdxAssetsInfo{
				Ticker:  assets.Ticker,
				Balance: 0,
			}
			for _, utxo := range assets.utxos {
				assetsInfo.Balance += int64(utxo.value)
			}

			assetsInfoList = append(assetsInfoList, assetsInfo)
		}

		return assetsInfoList, nil
	}
	err = errors.New("ordx assets not found")
	return nil, err
}

func (wallet *BTCWallet) getOrdxAssets(adddress string, utxo string) (*wire.OutPoint, error) {
	for _, addressDetails := range wallet.addressList {
		if addressDetails.address == adddress {
			// The receiving address cannot be used as a payment address
			for _, assets := range addressDetails.ordxAssets {
				utxoStrs := strings.Split(utxo, ":")
				if len(utxoStrs) != 2 {
					err := errors.New("invalid utxo")
					return nil, err
				}
				txid := utxoStrs[0]
				vout, _ := strconv.Atoi(utxoStrs[1])
				op := &wire.OutPoint{}
				Hash, err := chainhash.NewHashFromStr(txid)
				if err != nil {
					fmt.Printf("GetBTCUtoxs: Read response failed: %v\n", err)
					return nil, err
				}
				op.Hash = *Hash
				op.Index = uint32(vout)
				_, ok := assets.utxos[*op]
				if ok {
					return op, nil
				}
			}

		}
	}

	err := errors.New("cannot found the specific utxo for ordx")
	return nil, err
}

func LogPsbt(pb *psbt.Packet) {
	logLine("psbt:")
	logLine("UnsignedTx:")
	LogMsgTx(pb.UnsignedTx)

	//logLine("Inputs: %v", pb.Inputs)
	logLine("pb inputs account: %d", len(pb.Inputs))
	for index, input := range pb.Inputs {
		logLine("pb inputs index: %d", index)
		logpbInout(&input)
	}

	//logLine("Outputs: %v", pb.Outputs)
	logLine("pb outputs account: %d", len(pb.Outputs))
	for index, output := range pb.Outputs {
		logLine("pb outputs index: %d", index)
		logpbOutout(&output)
	}

	logLine("Unknowns: %+v", pb.Unknowns)

}

func LogMsgTx(tx *wire.MsgTx) {
	logLine("tx:%s", tx.TxHash().String())
	logLine("txin: %d", len(tx.TxIn))
	for index, txin := range tx.TxIn {
		logLine("		txin index: %d", index)
		logLine("		txin utxo txid: %s", txin.PreviousOutPoint.Hash.String())
		logLine("		txin utxo index: %d", txin.PreviousOutPoint.Index)
		logLine("		txin utxo Wintness: ")
		logLine("		{")
		for _, witness := range txin.Witness {
			logLine("		%x", witness)
		}
		logLine("		}")
		logLine("		txin SignatureScript: %x", txin.SignatureScript)
		logLine("		---------------------------------")
	}

	logLine("txout: %d", len(tx.TxOut))
	for index, txout := range tx.TxOut {
		logLine("		txout index: %d", index)
		logLine("		txout pkscript: %x", txout.PkScript)

		if txscript.IsNullData(txout.PkScript) {
			logLine("		txout pkscript is an OP_RETURN script")
		} else {
			addr, err := PkScriptToAddr(txout.PkScript)
			if err != nil {
				logLine("		txout pkscript is an invalidaddress: %s", err)
			} else {
				logLine("		txout address: %s", addr)
			}
		}
		logLine("		txout value: %d", txout.Value)
		logTxAssets("", txout.Assets)
		logLine("		---------------------------------")
	}
}

func logpbInout(pbinput *psbt.PInput) {
	logLine("pbinput:")
	if pbinput.NonWitnessUtxo != nil {
		logLine("pbinput.NonWitnessUtxo:")
		LogMsgTx(pbinput.NonWitnessUtxo)
	}

	if pbinput.WitnessUtxo != nil {
		logLine("pbinput.WitnessUtxo:")
		logLine("		pkScript: %x", pbinput.WitnessUtxo.PkScript)
		addr, err := PkScriptToAddr(pbinput.WitnessUtxo.PkScript)
		if err != nil {
			logLine("		txout pkscript is an invalidaddress: %s", err)
		} else {
			logLine("		txout address: %s", addr)
		}
		logLine("		txout value: %d", pbinput.WitnessUtxo.Value)
	}

	for index, partialSig := range pbinput.PartialSigs {
		logLine("	index %d partialSig: %x", index, partialSig)
	}

	logLine("		SighashType: %d", pbinput.SighashType)
	logLine("		RedeemScript: %x", pbinput.RedeemScript)
	logLine("		SighashType: %x", pbinput.SighashType)
	for index, bip32Derivation := range pbinput.Bip32Derivation {
		logLine("	index %d Bip32Derivation: %x", index, bip32Derivation)
	}

	logLine("		FinalScriptSig: %x", pbinput.FinalScriptSig)
	logLine("		FinalScriptWitness: %x", pbinput.FinalScriptWitness)
	logLine("		TaprootKeySpendSig: %x", pbinput.TaprootKeySpendSig)
	logLine("		SighashType: %d", pbinput.SighashType)

}

func logpbOutout(pboutput *psbt.POutput) {
	logLine("pboutput:")
	logLine("		RedeemScript: %x", pboutput.RedeemScript)
	logLine("		WitnessScript: %x", pboutput.WitnessScript)
	for index, bip32Derivation := range pboutput.Bip32Derivation {
		logLine("	index %d Bip32Derivation: %x", index, bip32Derivation)
	}
	logLine("		TaprootInternalKey: %x", pboutput.TaprootInternalKey)
	logLine("		TaprootTapTree: %x", pboutput.TaprootTapTree)

	for index, bip32Derivation := range pboutput.TaprootBip32Derivation {
		logLine("	index %d TaprootBip32Derivation: %x", index, bip32Derivation)
	}

}

func logTxAssets(desc string, assets wire.TxAssets) {
	if desc != "" {
		logLine("       	TxAssets desc: %s", desc)
	}
	logLine("       	TxAssets count: %d", len(assets))
	for index, asset := range assets {
		logLine("		---------------------------------")
		logLine("			TxAssets index: %d", index)
		logLine("			TxAssets Name Protocol: %s", asset.Name.Protocol)
		logLine("			TxAssets Name Type: %s", asset.Name.Type)
		logLine("			TxAssets Name Ticker: %s", asset.Name.Ticker)
		logLine("			TxAssets Amount: %s", asset.Amount.String())
		logLine("			TxAssets BindingSat: %d", asset.BindingSat)
	}

}

func logLine(format string, a ...any) {
	logStr := fmt.Sprintf(format, a...)
	fmt.Println(logStr)
}
