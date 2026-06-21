package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type output struct {
	TxID           string           `json:"txid"`
	ContractType   string           `json:"contract_type,omitempty"`
	PayloadBytes   int              `json:"payload_bytes"`
	PayloadOutputs []payloadOutput  `json:"payload_outputs,omitempty"`
	Deploy         *deployOutput    `json:"deploy,omitempty"`
	Invoke         *invokeOutput    `json:"invoke,omitempty"`
	Result         *resultOutput    `json:"result,omitempty"`
	StateRoot      *stateRootOutput `json:"state_root,omitempty"`
	RawPayloadHex  string           `json:"raw_payload_hex,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
}

type payloadOutput struct {
	Vout         int    `json:"vout"`
	TxType       string `json:"tx_type"`
	PayloadBytes int    `json:"payload_bytes"`
}

type deployOutput struct {
	Type               byte        `json:"type"`
	SubType            string      `json:"sub_type"`
	Version            uint32      `json:"version"`
	GasLimit           int64       `json:"gas_limit"`
	DeployNonce        uint64      `json:"deploy_nonce"`
	ContractContentHex string      `json:"contract_content_hex,omitempty"`
	ContractContent    interface{} `json:"contract_content,omitempty"`
}

type invokeOutput struct {
	GasLimit    int64       `json:"gas_limit"`
	CallNonce   uint64      `json:"call_nonce"`
	Action      string      `json:"action"`
	ParamHex    string      `json:"param_hex,omitempty"`
	ParamString string      `json:"param_string,omitempty"`
	Param       interface{} `json:"param,omitempty"`
}

type resultOutput struct {
	Status       string `json:"status"`
	StatusID     byte   `json:"status_id"`
	ResultCount  uint16 `json:"result_count"`
	HasErrorInfo bool   `json:"has_error_info"`
	ErrorDigest  string `json:"error_digest,omitempty"`
}

type stateRootOutput struct {
	StateRoot string `json:"state_root"`
}

func main() {
	netName := flag.String("net", "testnet", "satsnet network: mainnet or testnet")
	indexerBase := flag.String("indexer", "", "indexer base URL, overrides -net default")
	rawInput := flag.Bool("raw", false, "treat input as raw transaction hex instead of txid")
	showRaw := flag.Bool("raw-payload", false, "include concatenated contract payload hex")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] <txid>\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "       %s -raw [flags] <rawtxhex>\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "If input is omitted, it is read from stdin.")
		flag.PrintDefaults()
	}
	flag.Parse()

	input, err := readInput(flag.Args())
	if err != nil {
		exitErr(err)
	}
	rawHex := input
	if !*rawInput {
		rawHex, err = fetchRawTx(*netName, *indexerBase, input)
		if err != nil {
			exitErr(err)
		}
	}
	txBytes, err := hex.DecodeString(rawHex)
	if err != nil {
		exitErr(fmt.Errorf("decode raw tx hex: %w", err))
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(txBytes)); err != nil {
		exitErr(fmt.Errorf("deserialize tx: %w", err))
	}
	decoded, err := decodeContractOPReturn(&tx)
	if err != nil {
		exitErr(err)
	}
	if *showRaw {
		decoded.RawPayloadHex = hex.EncodeToString(concatPayloads(&tx))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(decoded); err != nil {
		exitErr(err)
	}
}

func readInput(args []string) (string, error) {
	var input string
	if len(args) > 0 {
		input = args[0]
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		input = string(data)
	}
	input = strings.TrimSpace(input)
	input = strings.Trim(input, "\"'")
	input = strings.Join(strings.Fields(input), "")
	if input == "" {
		return "", fmt.Errorf("missing txid or raw tx hex")
	}
	return input, nil
}

func fetchRawTx(netName, indexerBase, txid string) (string, error) {
	if indexerBase == "" {
		var err error
		indexerBase, err = defaultIndexerBase(netName)
		if err != nil {
			return "", err
		}
	}
	url := strings.TrimRight(indexerBase, "/") + "/btc/rawtx/" + txid
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch raw tx from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("fetch raw tx from %s: HTTP %s: %s",
			url, resp.Status, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("decode raw tx response: %w", err)
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("indexer returned code %d: %s", envelope.Code, envelope.Msg)
	}
	if strings.TrimSpace(envelope.Data) == "" {
		return "", fmt.Errorf("indexer returned empty raw tx")
	}
	return strings.TrimSpace(envelope.Data), nil
}

func defaultIndexerBase(netName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(netName)) {
	case "mainnet", "main", "satsnet":
		return "https://apiprd.ordx.market/satsnet/mainnet", nil
	case "testnet", "test":
		return "https://apiprd.ordx.market/satsnet/testnet", nil
	default:
		return "", fmt.Errorf("unsupported -net %q, want mainnet or testnet", netName)
	}
}

func decodeContractOPReturn(tx *wire.MsgTx) (output, error) {
	out := output{TxID: tx.TxID()}
	var txType contractcommon.TxType
	var payloadParts [][]byte
	for vout, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		partType, payload, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = partType
		} else if txType != partType {
			return output{}, fmt.Errorf("mixed contract OP_RETURN tx types: %s and %s",
				txTypeName(txType), txTypeName(partType))
		}
		payloadParts = append(payloadParts, payload)
		out.PayloadOutputs = append(out.PayloadOutputs, payloadOutput{
			Vout:         vout,
			TxType:       txTypeName(partType),
			PayloadBytes: len(payload),
		})
	}
	if txType == 0 {
		return output{}, fmt.Errorf("transaction has no contract OP_RETURN")
	}
	payload := bytes.Join(payloadParts, nil)
	out.ContractType = txTypeName(txType)
	out.PayloadBytes = len(payload)

	switch txType {
	case contractcommon.TxTypeDeploy:
		deploy, err := contractcommon.DecodeDeployPayload(payload)
		if err != nil {
			return output{}, err
		}
		out.Deploy = &deployOutput{
			Type:               deploy.Type,
			SubType:            deploy.SubType,
			Version:            deploy.Version,
			GasLimit:           deploy.GasLimit,
			DeployNonce:        deploy.DeployNonce,
			ContractContentHex: hex.EncodeToString(deploy.ContractContent),
			ContractContent:    decodeMaybeJSON(deploy.ContractContent),
		}
	case contractcommon.TxTypeInvoke:
		invoke, err := contractcommon.DecodeInvokePayload(payload)
		if err != nil {
			return output{}, err
		}
		out.Invoke = &invokeOutput{
			GasLimit:    invoke.GasLimit,
			CallNonce:   invoke.CallNonce,
			Action:      invoke.Action,
			ParamHex:    hex.EncodeToString(invoke.Param),
			ParamString: printableString(invoke.Param),
			Param:       decodeMaybeJSON(invoke.Param),
		}
	case contractcommon.TxTypeResult:
		result, err := contractcommon.DecodeResultPayload(payload)
		if err != nil {
			return output{}, err
		}
		out.Result = &resultOutput{
			Status:       resultStatusName(result.Status),
			StatusID:     byte(result.Status),
			ResultCount:  result.ResultCount,
			HasErrorInfo: result.HasErrorInfo,
		}
		if result.HasErrorInfo {
			out.Result.ErrorDigest = hex.EncodeToString(result.ErrorDigest[:])
		}
	case contractcommon.TxTypeCoinbaseStateRoot:
		root, err := contractcommon.DecodeStateRootPayload(payload)
		if err != nil {
			return output{}, err
		}
		out.StateRoot = &stateRootOutput{StateRoot: hex.EncodeToString(root.StateRoot[:])}
	default:
		return output{}, fmt.Errorf("unsupported contract tx type %d", txType)
	}
	return out, nil
}

func concatPayloads(tx *wire.MsgTx) []byte {
	var out []byte
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		_, payload, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		out = append(out, payload...)
	}
	return out
}

func decodeMaybeJSON(data []byte) interface{} {
	if len(data) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err == nil {
		return v
	}
	return nil
}

func printableString(data []byte) string {
	if len(data) == 0 || !json.Valid(data) {
		return ""
	}
	return string(data)
}

func txTypeName(txType contractcommon.TxType) string {
	switch txType {
	case contractcommon.TxTypeDeploy:
		return "deploy"
	case contractcommon.TxTypeInvoke:
		return "invoke"
	case contractcommon.TxTypeResult:
		return "result"
	case contractcommon.TxTypeCoinbaseStateRoot:
		return "coinbase_state_root"
	default:
		return fmt.Sprintf("unknown_%d", txType)
	}
}

func resultStatusName(status contractcommon.ResultStatus) string {
	switch status {
	case contractcommon.ResultStatusSuccess:
		return "success"
	case contractcommon.ResultStatusRevert:
		return "revert"
	case contractcommon.ResultStatusOutOfGas:
		return "out_of_gas"
	case contractcommon.ResultStatusInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("unknown_%d", status)
	}
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
