# DKVS validation report

- Focused tests: 1
- Race tests: 1
- Repository compile check: 1

## Focused tests
```text
go: downloading golang.org/x/crypto v0.44.0
go: downloading github.com/davecgh/go-spew v1.1.1
go: downloading github.com/stretchr/testify v1.11.1
go: downloading github.com/gin-gonic/gin v1.10.0
go: downloading github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd
go: downloading github.com/decred/dcrd/lru v1.1.3
go: downloading github.com/sirupsen/logrus v1.9.3
go: downloading github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
go: downloading github.com/gin-contrib/sse v0.1.0
go: downloading github.com/mattn/go-isatty v0.0.20
go: downloading golang.org/x/net v0.47.0
go: downloading golang.org/x/sys v0.40.0
go: downloading github.com/sat20-labs/btcd v0.24.3-beta-rc1
go: downloading github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792
go: downloading github.com/go-playground/validator/v10 v10.20.0
go: downloading github.com/pelletier/go-toml/v2 v2.2.2
go: downloading github.com/ugorji/go/codec v1.2.12
go: downloading google.golang.org/protobuf v1.36.11
go: downloading gopkg.in/yaml.v3 v3.0.1
go: downloading github.com/pmezard/go-difflib v1.0.0
go: downloading github.com/decred/dcrd/crypto/blake256 v1.0.1
go: downloading github.com/gabriel-vasile/mimetype v1.4.6
go: downloading github.com/go-playground/universal-translator v0.18.1
go: downloading github.com/leodido/go-urn v1.4.0
go: downloading golang.org/x/text v0.31.0
go: downloading github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0
go: downloading github.com/btcsuite/btcd/btcec/v2 v2.3.4
go: downloading github.com/sat20-labs/btcd/btcutil v1.1.7
go: downloading github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f
go: downloading github.com/go-playground/locales v0.14.1
# github.com/sat20-labs/satoshinet/indexer/indexer/dkvs
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/dkvs [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer [setup failed]
# github.com/sat20-labs/satoshinet/wire
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/wire [setup failed]
# github.com/sat20-labs/satoshinet/peer
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/peer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/peer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/peer [setup failed]
FAIL
```

## Race tests
```text
# github.com/sat20-labs/satoshinet/indexer/indexer/dkvs
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/dkvs [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
indexer/indexer/dkvs/authority.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
indexer/rpcserver/indexer/handler.go:14:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer [setup failed]
FAIL
```

## Compile check
```text
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/contract/node
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/contract/node [setup failed]
# github.com/sat20-labs/satoshinet/contract/oracle
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/contract/oracle
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/contract/oracle
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/contract/oracle [setup failed]
# github.com/sat20-labs/satoshinet/contract/template
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/contract/template [setup failed]
# github.com/sat20-labs/satoshinet/database
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/database [setup failed]
# github.com/sat20-labs/satoshinet/database/cmd/dbtool
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/database/cmd/dbtool [setup failed]
# github.com/sat20-labs/satoshinet/database/ffldb
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/database/ffldb [setup failed]
# github.com/sat20-labs/satoshinet/httpclient
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/httpclient
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/httpclient [setup failed]
# github.com/sat20-labs/satoshinet/indexer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer [setup failed]
# github.com/sat20-labs/satoshinet/indexer/common
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/common [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer/base
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer/base
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer/base
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/base [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer/contract
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer/contract
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/contract [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer/dkvs
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/dkvs [setup failed]
# github.com/sat20-labs/satoshinet/indexer/indexer/stp
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/indexer/stp
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/indexer/stp [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver/satoshinet
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver/satoshinet
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver/satoshinet [setup failed]
# github.com/sat20-labs/satoshinet/indexer/rpcserver/wire
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/indexer/rpcserver/wire
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/rpcserver/wire [setup failed]
# github.com/sat20-labs/satoshinet/indexer/share/indexer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/share/indexer [setup failed]
# github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc [setup failed]
# github.com/sat20-labs/satoshinet/integration/rpctest
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/integration/rpctest
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/integration/rpctest
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/integration/rpctest [setup failed]
# github.com/sat20-labs/satoshinet/mempool
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mempool
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mempool
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/mempool [setup failed]
# github.com/sat20-labs/satoshinet/mining
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/mining [setup failed]
# github.com/sat20-labs/satoshinet/mining/cpuminer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining/cpuminer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining/cpuminer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/mining/cpuminer [setup failed]
# github.com/sat20-labs/satoshinet/mining/posminer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining/posminer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/mining/posminer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/mining/posminer [setup failed]
# github.com/sat20-labs/satoshinet/mining/posminer/bootstrapnode
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/mining/posminer/bootstrapnode [setup failed]
# github.com/sat20-labs/satoshinet/netsync
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/netsync
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/netsync
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/netsync [setup failed]
# github.com/sat20-labs/satoshinet/peer
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/peer
httpclient/indexerclient.go:9:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
# github.com/sat20-labs/satoshinet/peer
indexer/indexer/db.go:7:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/peer [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincorehttp
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincorehttp [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincorehttpbulk
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincorehttpbulk [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincoreunixsocket
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/bitcoincoreunixsocket [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/btcdwebsockets
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/btcdwebsockets [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/btcwalletwebsockets
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/btcwalletwebsockets [setup failed]
# github.com/sat20-labs/satoshinet/rpcclient/examples/customcommand
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/rpcclient/examples/customcommand [setup failed]
# github.com/sat20-labs/satoshinet/stp
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/stp [setup failed]
# github.com/sat20-labs/satoshinet/stp/wallet/plugin
stp/wallet/plugin/plugin.go:7:2: github.com/sat20-labs/sat20wallet/sdk@v0.0.0: replacement directory ../sat20wallet/sdk does not exist
# github.com/sat20-labs/satoshinet/stp/wallet/plugin
stp/wallet/plugin/plugin.go:8:2: github.com/sat20-labs/sat20wallet/sdk@v0.0.0: replacement directory ../sat20wallet/sdk does not exist
# github.com/sat20-labs/satoshinet/stp/wallet/plugin
stp/wallet/plugin/plugin.go:9:2: github.com/sat20-labs/sat20wallet/sdk@v0.0.0: replacement directory ../sat20wallet/sdk does not exist
# github.com/sat20-labs/satoshinet/stp/wallet/plugin
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/stp/wallet/plugin [setup failed]
# github.com/sat20-labs/satoshinet/txscript
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/txscript [setup failed]
# github.com/sat20-labs/satoshinet/wire
btcd.go:23:2: github.com/sat20-labs/indexer@v0.3.0-20240926: replacement directory ../indexer does not exist
FAIL	github.com/sat20-labs/satoshinet/wire [setup failed]
ok  	github.com/sat20-labs/satoshinet/blockchain/internal/workmath	0.002s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcec	0.007s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcec/ecdsa	0.007s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcec/schnorr	0.002s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcec/schnorr/musig2	0.004s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcutil/base58	0.745s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/btcutil/bech32	0.003s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/chaincfg/chainhash	0.002s [no tests to run]
ok  	github.com/sat20-labs/satoshinet/database/internal/treap	0.003s [no tests to run]
?   	github.com/sat20-labs/satoshinet/indexer/indexer/dos	[no test files]
?   	github.com/sat20-labs/satoshinet/integration	[no test files]
?   	github.com/sat20-labs/satoshinet/limits	[no test files]
?   	github.com/sat20-labs/satoshinet/mining/posminer/utils	[no test files]
?   	github.com/sat20-labs/satoshinet/ossec	[no test files]
?   	github.com/sat20-labs/satoshinet/stp/transcend/plugin	[no test files]
FAIL
```
