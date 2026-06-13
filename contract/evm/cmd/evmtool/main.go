package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/wire"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "encode-address":
		encodeAddress(os.Args[2:])
	case "decode-address":
		decodeAddress(os.Args[2:])
	case "derive-create-address":
		deriveCreateAddress(os.Args[2:])
	case "derive-trigger-call-id":
		deriveTriggerCallID(os.Args[2:])
	case "encode-contract-script":
		encodeContractScript(os.Args[2:])
	case "decode-contract-script":
		decodeContractScript(os.Args[2:])
	case "encode-deploy":
		encodeDeploy(os.Args[2:])
	case "decode-deploy":
		decodeDeploy(os.Args[2:])
	case "encode-invoke":
		encodeInvoke(os.Args[2:])
	case "decode-invoke":
		decodeInvoke(os.Args[2:])
	case "encode-result":
		encodeResult(os.Args[2:])
	case "decode-result":
		decodeResult(os.Args[2:])
	case "encode-state-root":
		encodeStateRoot(os.Args[2:])
	case "decode-state-root":
		decodeStateRoot(os.Args[2:])
	case "encode-asset-balance":
		encodeAssetBalance(os.Args[2:])
	case "encode-asset-transfer":
		encodeAssetTransfer(os.Args[2:])
	case "decode-asset-transfer":
		decodeAssetTransfer(os.Args[2:])
	case "encode-trigger-height":
		encodeTriggerHeight(os.Args[2:])
	case "decode-trigger":
		decodeTrigger(os.Args[2:])
	case "build-deploy-tx":
		buildDeployTx(os.Args[2:])
	case "build-invoke-tx":
		buildInvokeTx(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func encodeAddress(args []string) {
	fs := flag.NewFlagSet("encode-address", flag.ExitOnError)
	prefix := fs.String("prefix", evm.TestnetContractPrefix, "contract address prefix: ca or tc")
	typ := fs.Uint("type", uint(evm.ContractTypeEVM), "contract type")
	hexAddr := fs.String("evm", "", "20-byte EVM address hex")
	_ = fs.Parse(args)
	addr, err := evm.ParseEVMAddressHex(*hexAddr)
	exitIfErr(err)
	contract, err := evm.NewContractAddress(*prefix, evm.AddressVersionV1, byte(*typ), addr)
	exitIfErr(err)
	encoded, err := contract.Encode()
	exitIfErr(err)
	fmt.Println(encoded)
}

func decodeAddress(args []string) {
	fs := flag.NewFlagSet("decode-address", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		exitIfErr(fmt.Errorf("decode-address requires one address argument"))
	}
	contract, err := evm.DecodeContractAddress(fs.Arg(0))
	exitIfErr(err)
	fmt.Printf("prefix=%s\n", contract.Prefix())
	fmt.Printf("version=%d\n", contract.Version())
	fmt.Printf("type=%d\n", contract.ContractType())
	fmt.Printf("evm=%s\n", evm.ContractAddressHash(contract).String())
}

func encodeContractScript(args []string) {
	fs := flag.NewFlagSet("encode-contract-script", flag.ExitOnError)
	address := fs.String("address", "", "ca/tc contract address")
	_ = fs.Parse(args)
	contract, err := evm.DecodeContractAddress(*address)
	exitIfErr(err)
	script, err := evm.ContractPkScript(contract)
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(script))
}

func decodeContractScript(args []string) {
	fs := flag.NewFlagSet("decode-contract-script", flag.ExitOnError)
	prefix := fs.String("prefix", evm.TestnetContractPrefix, "contract address prefix: ca or tc")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		exitIfErr(fmt.Errorf("decode-contract-script requires one script hex argument"))
	}
	script, err := hex.DecodeString(trimHexPrefix(fs.Arg(0)))
	exitIfErr(err)
	contract, ok, err := evm.ParseContractPkScript(script, *prefix)
	exitIfErr(err)
	if !ok {
		exitIfErr(fmt.Errorf("not an EVM contract pkScript"))
	}
	encoded, err := contract.Encode()
	exitIfErr(err)
	fmt.Printf("contract=%s\n", encoded)
	fmt.Printf("evm=%s\n", evm.ContractAddressHash(contract).String())
}

func deriveCreateAddress(args []string) {
	fs := flag.NewFlagSet("derive-create-address", flag.ExitOnError)
	prefix := fs.String("prefix", evm.TestnetContractPrefix, "contract address prefix: ca or tc")
	callerHex := fs.String("caller", "", "20-byte caller EVM address hex")
	nonce := fs.Uint64("nonce", 0, "creator nonce")
	_ = fs.Parse(args)
	caller, err := evm.ParseEVMAddressHex(*callerHex)
	exitIfErr(err)
	contract, err := evm.DeriveCreateContractAddress(*prefix, caller, *nonce)
	exitIfErr(err)
	encoded, err := contract.Encode()
	exitIfErr(err)
	fmt.Printf("contract=%s\n", encoded)
	fmt.Printf("evm=%s\n", evm.ContractAddressHash(contract).String())
}

func deriveTriggerCallID(args []string) {
	fs := flag.NewFlagSet("derive-trigger-call-id", flag.ExitOnError)
	contractAddress := fs.String("contract", "", "ca/tc contract address")
	triggerID := fs.String("trigger-id", "", "trigger identifier")
	height := fs.Int64("height", 0, "trigger block height")
	_ = fs.Parse(args)
	contract, err := evm.DecodeContractAddress(*contractAddress)
	exitIfErr(err)
	if *triggerID == "" {
		exitIfErr(fmt.Errorf("missing trigger id"))
	}
	if *height < 0 {
		exitIfErr(fmt.Errorf("height must not be negative"))
	}
	fmt.Println(evm.DeriveTriggerCallID(contract, *triggerID, *height))
}

func encodeDeploy(args []string) {
	fs := flag.NewFlagSet("encode-deploy", flag.ExitOnError)
	gasLimit := fs.Uint64("gas-limit", 0, "deploy gas limit")
	nonce := fs.Uint64("nonce", 0, "deploy nonce")
	initCodeHex := fs.String("init-code", "", "EVM init code hex")
	_ = fs.Parse(args)
	initCode, err := hex.DecodeString(trimHexPrefix(*initCodeHex))
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(evmcommon.EncodeDeployPayload(evm.DeployPayload{
		GasLimit:    *gasLimit,
		DeployNonce: *nonce,
		InitCode:    initCode,
	})))
}

func decodeDeploy(args []string) {
	data := decodeSingleHexArg("decode-deploy", args)
	payload, err := evmcommon.DecodeDeployPayload(data)
	exitIfErr(err)
	fmt.Printf("gas_limit=%d\n", payload.GasLimit)
	fmt.Printf("nonce=%d\n", payload.DeployNonce)
	fmt.Printf("init_code=%x\n", payload.InitCode)
}

func encodeInvoke(args []string) {
	fs := flag.NewFlagSet("encode-invoke", flag.ExitOnError)
	gasLimit := fs.Uint64("gas-limit", 0, "invoke gas limit")
	nonce := fs.Uint64("nonce", 0, "call nonce")
	calldataHex := fs.String("calldata", "", "EVM calldata hex")
	_ = fs.Parse(args)
	calldata, err := hex.DecodeString(trimHexPrefix(*calldataHex))
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(evmcommon.EncodeInvokePayload(evm.InvokePayload{
		GasLimit:  *gasLimit,
		CallNonce: *nonce,
		Calldata:  calldata,
	})))
}

func decodeInvoke(args []string) {
	data := decodeSingleHexArg("decode-invoke", args)
	payload, err := evmcommon.DecodeInvokePayload(data)
	exitIfErr(err)
	fmt.Printf("gas_limit=%d\n", payload.GasLimit)
	fmt.Printf("nonce=%d\n", payload.CallNonce)
	fmt.Printf("calldata=%x\n", payload.Calldata)
}

func encodeResult(args []string) {
	fs := flag.NewFlagSet("encode-result", flag.ExitOnError)
	status := fs.Uint("status", uint(evm.ResultStatusSuccess), "result status")
	count := fs.Uint("count", 1, "batch result count")
	errorDigestHex := fs.String("error-digest", "", "optional 32-byte error digest hex")
	_ = fs.Parse(args)
	var digest [32]byte
	hasDigest := *errorDigestHex != ""
	if hasDigest {
		decoded, err := hex.DecodeString(trimHexPrefix(*errorDigestHex))
		exitIfErr(err)
		if len(decoded) != 32 {
			exitIfErr(fmt.Errorf("error digest must be 32 bytes"))
		}
		copy(digest[:], decoded)
	}
	if *count > uint(^uint16(0)) {
		exitIfErr(fmt.Errorf("count overflows uint16"))
	}
	fmt.Println(hex.EncodeToString(evmcommon.EncodeResultPayload(evm.ResultPayload{
		Status:       evm.ResultStatus(byte(*status)),
		ResultCount:  uint16(*count),
		ErrorDigest:  digest,
		HasErrorInfo: hasDigest,
	})))
}

func decodeResult(args []string) {
	data := decodeSingleHexArg("decode-result", args)
	payload, err := evmcommon.DecodeResultPayload(data)
	exitIfErr(err)
	fmt.Printf("status=%d\n", payload.Status)
	fmt.Printf("count=%d\n", payload.ResultCount)
	if payload.HasErrorInfo {
		fmt.Printf("error_digest=%x\n", payload.ErrorDigest)
	}
}

func encodeStateRoot(args []string) {
	fs := flag.NewFlagSet("encode-state-root", flag.ExitOnError)
	rootHex := fs.String("root", "", "32-byte state root hex")
	_ = fs.Parse(args)
	decoded, err := hex.DecodeString(trimHexPrefix(*rootHex))
	exitIfErr(err)
	if len(decoded) != 32 {
		exitIfErr(fmt.Errorf("state root must be 32 bytes"))
	}
	var root [32]byte
	copy(root[:], decoded)
	fmt.Println(hex.EncodeToString(evmcommon.EncodeStateRootPayload(evm.StateRootPayload{StateRoot: root})))
}

func decodeStateRoot(args []string) {
	data := decodeSingleHexArg("decode-state-root", args)
	payload, err := evmcommon.DecodeStateRootPayload(data)
	exitIfErr(err)
	fmt.Printf("state_root=%x\n", payload.StateRoot)
}

func encodeAssetBalance(args []string) {
	fs := flag.NewFlagSet("encode-asset-balance", flag.ExitOnError)
	ownerHex := fs.String("owner", "", "20-byte owner EVM address hex")
	assetName := fs.String("asset", evm.SatoshiAssetName, "asset name")
	_ = fs.Parse(args)
	owner, err := evm.ParseEVMAddressHex(*ownerHex)
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(evm.EncodeBalanceOfCall(owner, *assetName)))
}

func encodeAssetTransfer(args []string) {
	fs := flag.NewFlagSet("encode-asset-transfer", flag.ExitOnError)
	assetName := fs.String("asset", evm.SatoshiAssetName, "asset name")
	to := fs.String("to", "", "destination address")
	amount := fs.String("amount", "0", "asset amount in decimal string form")
	extraHex := fs.String("extra", "", "optional extra data hex")
	_ = fs.Parse(args)
	extra, err := hex.DecodeString(trimHexPrefix(*extraHex))
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(evm.EncodeTransferAssetCall(*assetName, *to, *amount, extra)))
}

func decodeAssetTransfer(args []string) {
	data := decodeSingleHexArg("decode-asset-transfer", args)
	assetName, to, amount, extraData, err := evm.DecodeTransferAssetCall(data)
	exitIfErr(err)
	fmt.Printf("asset=%s\n", assetName)
	fmt.Printf("to=%s\n", to)
	fmt.Printf("amount=%s\n", amount.String())
	fmt.Printf("extra=%x\n", extraData)
}

func encodeTriggerHeight(args []string) {
	fs := flag.NewFlagSet("encode-trigger-height", flag.ExitOnError)
	triggerID := fs.String("id", "", "trigger identifier")
	height := fs.Uint64("height", 0, "trigger block height")
	gasLimit := fs.Uint64("gas-limit", 0, "trigger execution gas limit")
	calldataHex := fs.String("calldata", "", "trigger calldata hex")
	_ = fs.Parse(args)
	calldata, err := hex.DecodeString(trimHexPrefix(*calldataHex))
	exitIfErr(err)
	fmt.Println(hex.EncodeToString(evm.EncodeRegisterHeightTriggerCall(*triggerID, *height, *gasLimit, calldata)))
}

func decodeTrigger(args []string) {
	data := decodeSingleHexArg("decode-trigger", args)
	trigger, err := evm.DecodeTriggerRegistrationCall(data)
	exitIfErr(err)
	fmt.Printf("id=%s\n", trigger.ID)
	fmt.Printf("kind=%d\n", trigger.Kind)
	fmt.Printf("height=%d\n", trigger.Height)
	fmt.Printf("gas_limit=%d\n", trigger.GasLimit)
	fmt.Printf("calldata=%x\n", trigger.Calldata)
}

func buildDeployTx(args []string) {
	fs := flag.NewFlagSet("build-deploy-tx", flag.ExitOnError)
	prefix := fs.String("prefix", evm.TestnetContractPrefix, "contract address prefix: ca or tc")
	callerHex := fs.String("caller", "", "20-byte caller EVM address hex")
	gasLimit := fs.Uint64("gas-limit", 0, "deploy gas limit")
	nonce := fs.Uint64("nonce", 0, "deploy nonce")
	initCodeHex := fs.String("init-code", "", "EVM init code hex")
	fundSats := fs.Int64("fund-sats", 0, "satoshi amount sent to contract")
	var inputs repeatedFlag
	var fundAssets repeatedFlag
	fs.Var(&inputs, "input", "input outpoint txid:vout, repeatable")
	fs.Var(&fundAssets, "fund-asset", "asset funding assetName=amount, repeatable")
	_ = fs.Parse(args)

	caller, err := evm.ParseEVMAddressHex(*callerHex)
	exitIfErr(err)
	initCode, err := hex.DecodeString(trimHexPrefix(*initCodeHex))
	exitIfErr(err)
	inputOutpoints := parseOutPoints(inputs)
	assets := parseAssetAmounts(fundAssets)
	tx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: *prefix,
		Caller:         caller,
		GasLimit:       *gasLimit,
		DeployNonce:    *nonce,
		InitCode:       initCode,
		Funding:        wire.TxOut{Value: *fundSats, Assets: assets},
		Inputs:         inputOutpoints,
	})
	exitIfErr(err)
	txHex, err := evm.MsgTxHex(tx)
	exitIfErr(err)
	fmt.Printf("contract=%s\n", contract.MustEncode())
	fmt.Printf("tx=%s\n", txHex)
}

func buildInvokeTx(args []string) {
	fs := flag.NewFlagSet("build-invoke-tx", flag.ExitOnError)
	contractAddress := fs.String("contract", "", "ca/tc contract address")
	gasLimit := fs.Uint64("gas-limit", 0, "invoke gas limit")
	nonce := fs.Uint64("nonce", 0, "call nonce")
	calldataHex := fs.String("calldata", "", "EVM calldata hex")
	fundSats := fs.Int64("fund-sats", 0, "satoshi amount sent to contract as msg.value")
	var inputs repeatedFlag
	var fundAssets repeatedFlag
	fs.Var(&inputs, "input", "input outpoint txid:vout, repeatable")
	fs.Var(&fundAssets, "fund-asset", "asset funding assetName=amount, repeatable")
	_ = fs.Parse(args)

	contract, err := evm.DecodeContractAddress(*contractAddress)
	exitIfErr(err)
	calldata, err := hex.DecodeString(trimHexPrefix(*calldataHex))
	exitIfErr(err)
	tx, err := evm.BuildInvokeTx(evm.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  *gasLimit,
		CallNonce: *nonce,
		Calldata:  calldata,
		Funding:   wire.TxOut{Value: *fundSats, Assets: parseAssetAmounts(fundAssets)},
		Inputs:    parseOutPoints(inputs),
	})
	exitIfErr(err)
	txHex, err := evm.MsgTxHex(tx)
	exitIfErr(err)
	fmt.Printf("tx=%s\n", txHex)
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  evmtool encode-address -prefix tc -type 1 -evm 00112233445566778899aabbccddeeff00112233
  evmtool decode-address <contract-address>
  evmtool derive-create-address -prefix tc -caller 00112233445566778899aabbccddeeff00112233 -nonce 1
  evmtool derive-trigger-call-id -contract <contract-address> -trigger-id vault-release -height 100
  evmtool encode-contract-script -address <contract-address>
  evmtool decode-contract-script -prefix tc <script-hex>
  evmtool encode-deploy -gas-limit 100000 -nonce 1 -init-code 6080
  evmtool decode-deploy <payload-hex>
  evmtool encode-invoke -gas-limit 50000 -nonce 1 -calldata deadbeef
  evmtool decode-invoke <payload-hex>
  evmtool encode-result -status 0 -count 1 [-error-digest <32-byte-hex>]
  evmtool decode-result <payload-hex>
  evmtool encode-state-root -root <32-byte-hex>
  evmtool decode-state-root <payload-hex>
  evmtool encode-asset-balance -owner 00112233445566778899aabbccddeeff00112233 -asset ::
  evmtool encode-asset-transfer -asset :: -to tb1... -amount 1000 [-extra deadbeef]
  evmtool decode-asset-transfer <calldata-hex>
  evmtool encode-trigger-height -id vault-release -height 100 -gas-limit 50000 -calldata deadbeef
  evmtool decode-trigger <calldata-hex>
  evmtool build-deploy-tx -caller <evm> -nonce 1 -gas-limit 100000 -init-code 6080 -input <txid:vout> -fund-asset ordx:ft:gas=100000
  evmtool build-invoke-tx -contract <contract-address> -nonce 1 -gas-limit 50000 -calldata deadbeef -input <txid:vout> -fund-asset ordx:ft:gas=50000
`)
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func parseOutPoints(values []string) []wire.OutPoint {
	outpoints := make([]wire.OutPoint, 0, len(values))
	for _, value := range values {
		outpoint, err := evm.ParseOutPoint(value)
		exitIfErr(err)
		outpoints = append(outpoints, outpoint)
	}
	return outpoints
}

func parseAssetAmounts(values []string) wire.TxAssets {
	assets := make(wire.TxAssets, 0, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 {
			exitIfErr(fmt.Errorf("asset funding must be assetName=amount"))
		}
		decimal, err := evm.ParseDecimalAmountString(parts[1])
		exitIfErr(err)
		assets = append(assets, wire.AssetInfo{
			Name:   *wire.NewAssetNameFromString(parts[0]),
			Amount: *decimal,
		})
	}
	return assets
}

func decodeSingleHexArg(name string, args []string) []byte {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		exitIfErr(fmt.Errorf("%s requires one payload hex argument", name))
	}
	data, err := hex.DecodeString(trimHexPrefix(fs.Arg(0)))
	exitIfErr(err)
	return data
}

func trimHexPrefix(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
