package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/btcwallet"
	"github.com/sat20-labs/satoshinet/cmd/btcd_client/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/sirupsen/logrus"
)

const (
	showHelpMessage = "Specify -h to show available options"
	listCmdMessage  = "Specify -l to list available commands"
)

var (
	defaultHomeDir = btcutil.AppDataDir("satoshinet", false)
	currentNetwork = &chaincfg.MainNetParams
	currentCfg     = &config{}
)

//var log *logrus.Entry

// logWriter implements an io.Writer that outputs to both standard output and
// the write-end pipe of an initialized log rotator.
type logWriter struct{}

func (logWriter) Write(p []byte) (n int, err error) {
	os.Stdout.Write(p)
	return len(p), nil
}

var (
	// backendLog is the logging backend used to create all subsystem loggers.
	// The backend must not be used before the log rotator has been initialized,
	// or data races and/or nil pointer dereferences will occur.
	backendLog = logrus.New()

	log = backendLog.WithField("module", "CMD")
)

// commandUsage display the usage for a specific command.
func commandUsage(method string) {
	usage, err := btcjson.MethodUsageText(method)
	if err != nil {
		// This should never happen since the method was already checked
		// before calling this function, but be safe.
		fmt.Fprintln(os.Stderr, "Failed to obtain command usage:", err)
		return
	}

	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(os.Stderr, "  %s\n", usage)
}

// usage displays the general usage when the help flag is not displayed and
// and an invalid command was specified.  The commandUsage function is used
// instead when a valid command was specified.
func usage(errorMessage string) {
	appName := filepath.Base(os.Args[0])
	appName = strings.TrimSuffix(appName, filepath.Ext(appName))
	fmt.Fprintln(os.Stderr, errorMessage)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(os.Stderr, "  %s [OPTIONS] <command> <args...>\n\n",
		appName)
	fmt.Fprintln(os.Stderr, showHelpMessage)
	fmt.Fprintln(os.Stderr, listCmdMessage)
}

func main() {
	fmt.Printf("Starting btcd command is %v\n", os.Args)
	// homeDir := flag.String("homedir", defaultHomeDir, "all data path")
	// flag.Parse()
	cfg, args, err := loadConfig()
	if err != nil {
		os.Exit(1)
	}

	backendLog.SetLevel(logrus.DebugLevel)
	currentCfg = cfg

	//startDaemon()

	log.Debugf("**** satsnet homeDir is %v\n", cfg.HomeDir)
	fmt.Printf("args is %v\n", args)
	// if len(args) < 1 {
	// 	usage("No command specified")
	// 	os.Exit(1)
	// }

	// rpchost := "192.168.10.104"
	// rpcPost := 15827
	// rpcuser := "q17AIoqBJSEhW7djqjn0nTsZcz4="
	// rpcpass := "nnlkAZn58bqsyYwVtHIajZ16cj8="
	// certFileDir := "D:\\Work\\Tinyverse\\develop\\satsnet\\satoshinet\\cmd\\btcd_client\\btcd104"
	rpchost := "127.0.0.1"
	rpcPost := 19527
	rpcuser := cfg.RPCUser
	rpcpass := cfg.RPCPassword
	certFileDir := filepath.Join(cfg.HomeDir, "btcd")
	err = satsnet_rpc.InitSatsNetClient(rpchost, rpcPost, rpcuser, rpcpass, certFileDir)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	//btcwallet.InitBTCWallet("BTCWallet", btcctlHomeDir)
	btcwallet.InitWalletManager(btcctlHomeDir)
	connectRPC := RPC_BTCD
	if cfg.Wallet {
		connectRPC = RPC_WALLET
	}
	if connectRPC == RPC_WALLET {
		fmt.Println("Default RPC is btcwallet, call \"setrpc btcd\" to change rpc to btcd.")
	} else {
		fmt.Println("Default RPC is btcd, call \"setrpc wallet\" to change rpc to btcwallet.")
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\n\n\nwait for your input>>")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println(err)
			continue
		}

		words := strings.Fields(input)
		length := len(words)
		if length == 0 {
			continue
		}

		// Ensure the specified method identifies a valid registered command and
		// is one of the usable types.
		method := words[0]

		if method == "exit" {
			return
		} else if method == "anchortx" {
			lockedTTxid := ""
			address := ""
			assets := wire.TxAssets{}
			if length >= 2 {
				lockedTTxid = words[1]
			}
			if length >= 3 {
				address = words[2]
			}
			amount := int64(1000000)
			if length >= 4 {
				newAmout, _ := strconv.ParseInt(words[3], 10, 64)
				if newAmout != 0 {
					amount = newAmout
				}
			}

			if length >= 5 {
				assetProtocol := "ordx"
				assetsType := "ft"
				assetsTicker := words[4]
				assetAmount := amount
				if length >= 6 {
					newAssetsAmout, _ := strconv.ParseInt(words[5], 10, 64)
					if newAssetsAmout != 0 {
						assetAmount = newAssetsAmout
					}
				}
				bindingsSats := uint32(0)
				if length >= 7 {
					newBindingsSats, _ := strconv.ParseInt(words[6], 10, 16)
					if newBindingsSats != 0 {
						bindingsSats = uint32(newBindingsSats)
					}
				}
				asset := wire.AssetInfo{Name: wire.AssetName{Protocol: assetProtocol, Type: assetsType, Ticker: assetsTicker}, Amount: *common.NewDefaultDecimal(assetAmount), BindingSat: bindingsSats}
				assets = append(assets, asset)
			}
			testAnchorTx(lockedTTxid, address, amount, assets)
			continue
		} else if method == "parseanchorscript" {
			script := ""
			if length < 2 {
				fmt.Printf("parseanchorscript need anchorscript\n")
				continue
			}
			script = words[1]

			anchorScript, err := hex.DecodeString(script)
			if err != nil {
				fmt.Println(err)
				continue
			}
			testParseAnchorScript(anchorScript)
			continue
		} else if method == "rpcanchortx" {
			lockedTTxid := ""
			address := ""
			if length >= 2 {
				lockedTTxid = words[1]
			}
			if length >= 3 {
				address = words[2]
			}
			testrpcAnchorTx(lockedTTxid, address)
			continue
		} else if method == "importwallet" {
			walletName := ""
			if length > 1 {
				walletName = words[1]
			}
			testImportWallet(walletName)
			continue
		} else if method == "createwallet" {
			walletName := ""
			if length > 1 {
				walletName = words[1]
			}
			testCreateWallet(walletName)
			continue
		} else if method == "showaddress" {
			testShowAddress()
			continue
		} else if method == "transfer" {
			if length < 3 {
				fmt.Printf("transfer need address and amount\n")
				continue
			}
			address := words[1]
			amount, err := strconv.ParseInt(words[2], 10, 64)
			if err != nil {
				fmt.Printf("transfer need address and amount\n")
				continue
			}
			testTransfer(address, amount)
			continue
		} else if method == "unanchor" {
			if length < 3 {
				fmt.Printf("transfer need address and amount\n")
				continue
			}
			address := words[1]
			amount, err := strconv.ParseInt(words[2], 10, 64)
			if err != nil {
				fmt.Printf("transfer need address and amount\n")
				continue
			}
			testUnanchorTransfer(address, amount)
			continue
		} else if method == "setrpc" {
			if length < 2 {
				fmt.Printf("setrpc need rpc name: btcd or wallet\n")
				continue
			}
			rpcname := words[1]
			if rpcname == "btcd" {
				connectRPC = RPC_BTCD
				fmt.Println("setrpc to btcd success.")
			} else if rpcname == "wallet" {
				connectRPC = RPC_WALLET
				fmt.Println("setrpc to btcwallet success.")
			} else {
				fmt.Printf("setrpc need rpc name: btcd or wallet\n")
				continue
			}
			continue
		} else if method == "getblockstats" {
			if length < 3 {
				fmt.Printf("getblockstats need height and stats\n")
				continue
			}
			height, err := strconv.ParseInt(words[1], 10, 64)
			if err != nil {
				fmt.Printf("parse height error, need int\n")
			}
			stats := make([]string, 0)

			for i := 2; i < length; i++ {
				stats = append(stats, words[i])
			}

			testGetBlockStats(int(height), stats)
			continue
		} else if method == "getmempoolentry" {
			if length < 2 {
				fmt.Printf("getmempoolentry need txid\n")
				continue
			}
			txid := words[1]

			testGetMempoolEntry(txid)
			continue
		} else if method == "getrawtransaction" {
			if length < 2 {
				fmt.Printf("getrawtransaction need txid\n")
				continue
			}
			txid := words[1]

			testGetRawTransaction(txid)
			continue
		} else if method == "accordinglockedinfo" {
			if length < 2 {
				fmt.Printf("accordinglockedinfo need txid\n")
				continue
			}
			txid := words[1]

			testGetAccordingLockedInfo(txid)
			continue
		} else if method == "accordinganchorinfo" {
			if length < 2 {
				fmt.Printf("accordinganchorinfo need utxo\n")
				continue
			}
			utxo := words[1]

			testGetAccordingAnchorInfo(utxo)
			continue
		} else if method == "sendrawtransaction" {
			raw := ""
			if length >= 2 {
				raw = words[1]
			}
			testSendRawTransaction(raw)
			continue
		} else if method == "sendtxsample" {

			testSampleTx()
			continue
		} else if method == "showgenesis" {
			showGenesisBlock(currentNetwork)
			continue
		} else if method == "addr2pk" {
			if length < 2 {
				fmt.Printf("parseaddress need address\n")
				return
			}
			address := words[1]
			parseAddress(address, currentNetwork)
			continue
		} else if method == "pk2addr" {
			if length < 2 {
				fmt.Printf("parseaddress need address\n")
				return
			}
			pkscript := words[1]
			parsePkScript(pkscript, currentNetwork)
			continue
		} else if method == "creategenesis" {
			GenerateGenesisBlock(currentNetwork)
			continue
		} else if method == "showblocks" {
			start := int64(0)
			end := int64(-1)
			if length >= 2 {
				number, err := strconv.ParseInt(words[1], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				start = number
			}
			if length >= 3 {
				number, err := strconv.ParseInt(words[2], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				end = number
			}

			ShowBlocks(start, end)
			continue
		} else if method == "showblock" {
			start := int64(0)
			end := int64(-1)
			if length >= 2 {
				number, err := strconv.ParseInt(words[1], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				start = number
			}
			if length >= 3 {
				number, err := strconv.ParseInt(words[2], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				end = number
			}

			ShowBlocks(start, end)
			continue
		} else if method == "showvcblocks" {
			start := int64(0)
			end := int64(-1)
			if length >= 2 {
				number, err := strconv.ParseInt(words[1], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				start = number
			}
			if length >= 3 {
				number, err := strconv.ParseInt(words[2], 10, 64)

				if err != nil {
					fmt.Printf("parse height error, need int\n")
				}

				end = number
			}

			ShowVCBlocks(start, end)
			continue
		} else if method == "testrpcblocks" {
			fmt.Fprintln(os.Stderr, listCmdMessage)
			//os.Exit(0)
			TestRPCGetBlocks()
			continue
		} else if method == "gettxblock" {
			if length < 2 {
				fmt.Printf("accordinganchorinfo need txid\n")
				continue
			}
			txid := words[1]
			testGetBlocksWithTx(txid)
			continue
		} else if method == "getlockedutxoinfo" {
			if length < 2 {
				fmt.Printf("showtx need txid\n")
				continue
			}
			utxo := words[1]
			testGetLockedUtxoInfo(utxo)
			continue
		}

		// Ensure the number of arguments are within bounds.
		usageFlags, err := btcjson.MethodUsageFlags(method)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unrecognized command '%s'\n", method)
			fmt.Fprintln(os.Stderr, listCmdMessage)
			//os.Exit(1)
			continue
		}
		if usageFlags&unusableFlags != 0 {
			fmt.Fprintf(os.Stderr, "The '%s' command can only be used via "+
				"websockets\n", method)
			fmt.Fprintln(os.Stderr, listCmdMessage)
			//os.Exit(1)
			continue
		}

		// Convert remaining command line words to a slice of interface values
		// to be passed along as parameters to new command creation function.
		//
		// Since some commands, such as submitblock, can involve data which is
		// too large for the Operating System to allow as a normal command line
		// parameter, support using '-' as an argument to allow the argument
		// to be read from a stdin pipe.
		bio := bufio.NewReader(os.Stdin)
		params := make([]interface{}, 0, len(words[1:]))
		for _, arg := range words[1:] {
			if arg == "-" {
				param, err := bio.ReadString('\n')
				if err != nil && err != io.EOF {
					fmt.Fprintf(os.Stderr, "Failed to read data "+
						"from stdin: %v\n", err)
					os.Exit(1)
				}
				if err == io.EOF && len(param) == 0 {
					fmt.Fprintln(os.Stderr, "Not enough lines "+
						"provided on stdin")
					os.Exit(1)
				}
				param = strings.TrimRight(param, "\r\n")
				params = append(params, param)
				continue
			}

			params = append(params, arg)
		}

		// Attempt to create the appropriate command using the arguments
		// provided by the user.
		cmd, err := btcjson.NewCmd(method, params...)
		if err != nil {
			// Show the error along with its error code when it's a
			// btcjson.Error as it reallistcally will always be since the
			// NewCmd function is only supposed to return errors of that
			// type.
			if jerr, ok := err.(btcjson.Error); ok {
				fmt.Fprintf(os.Stderr, "%s command: %v (code: %s)\n",
					method, err, jerr.ErrorCode)
				commandUsage(method)
				//os.Exit(1)
				continue
			}

			// The error is not a btcjson.Error and this really should not
			// happen.  Nevertheless, fallback to just showing the error
			// if it should happen due to a bug in the package.
			fmt.Fprintf(os.Stderr, "%s command: %v\n", method, err)
			commandUsage(method)
			//os.Exit(1)
			continue
		}

		// Marshal the command into a JSON-RPC byte slice in preparation for
		// sending it to the RPC server.
		marshalledJSON, err := btcjson.MarshalCmd(btcjson.RpcVersion1, 1, cmd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			//os.Exit(1)
			continue
		}

		// Send the JSON-RPC request to the server using the user-specified
		// connection configuration.
		result, err := sendPostRequest(marshalledJSON, cfg, connectRPC)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			//os.Exit(1)
			continue
		}

		// Choose how to display the result based on its type.
		strResult := string(result)
		if strings.HasPrefix(strResult, "{") || strings.HasPrefix(strResult, "[") {
			var dst bytes.Buffer
			if err := json.Indent(&dst, result, "", "  "); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to format result: %v",
					err)
				//os.Exit(1)
				continue
			}
			fmt.Println(dst.String())

		} else if strings.HasPrefix(strResult, `"`) {
			var str string
			if err := json.Unmarshal(result, &str); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to unmarshal result: %v",
					err)
				//os.Exit(1)
				continue
			}
			fmt.Println(str)

		} else if strResult != "null" {
			fmt.Println(strResult)
		}
	}
}
