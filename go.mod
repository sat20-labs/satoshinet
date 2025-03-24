module github.com/sat20-labs/satoshinet

replace github.com/btcsuite/btcd => github.com/sat20-labs/btcd v0.24.3-beta-rc1

replace github.com/btcsuite/btcwallet => github.com/sat20-labs/btcwallet v0.16.11

replace github.com/btcsuite/btcd/btcutil => github.com/sat20-labs/btcd/btcutil v1.1.7

replace github.com/sat20-labs/indexer => ../indexer

require (
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792
	github.com/btcsuite/winsvc v1.0.0
	github.com/davecgh/go-spew v1.1.1
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
	github.com/decred/dcrd/lru v1.0.0
	github.com/dgraph-io/badger/v4 v4.5.0
	github.com/gin-contrib/cors v1.7.2
	github.com/gin-contrib/logger v1.1.2
	github.com/gin-gonic/gin v1.10.0
	github.com/gorilla/websocket v1.5.0
	github.com/jessevdk/go-flags v1.4.0
	github.com/lestrrat-go/file-rotatelogs v2.4.0+incompatible
	github.com/rs/zerolog v1.33.0
	github.com/sat20-labs/indexer v0.3.0-20240926
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
	github.com/swaggo/files v1.0.1
	github.com/swaggo/gin-swagger v1.6.0
	github.com/syndtr/goleveldb v1.0.1-0.20210819022825-2ae1ddf74ef7
	github.com/tyler-smith/go-bip39 v1.1.0
	go.etcd.io/bbolt v1.3.11
	golang.org/x/crypto v0.33.0
	golang.org/x/sys v0.30.0
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/OLProtocol/go-bitcoind v0.0.0-20240716001842-eaea89a7c02d // indirect
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/btcsuite/btcd/btcec/v2 v2.3.4 // indirect
	github.com/btcsuite/btcd/btcutil v1.1.6 // indirect
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0 // indirect
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f // indirect
	github.com/bytedance/sonic v1.11.6 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/dgraph-io/ristretto/v2 v2.0.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.6 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.6 // indirect
	github.com/go-openapi/spec v0.20.4 // indirect
	github.com/go-openapi/swag v0.19.15 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.20.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/flatbuffers v24.3.25+incompatible // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lestrrat-go/strftime v1.1.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/onsi/gomega v1.27.8 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rogpeppe/go-internal v1.10.0 // indirect
	github.com/swaggo/swag v1.16.3 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/net v0.35.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	golang.org/x/tools v0.28.0 // indirect
	google.golang.org/protobuf v1.36.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	lukechampine.com/uint128 v1.3.0 // indirect
)

require (
	github.com/aead/siphash v1.0.1
	github.com/btcsuite/btcd v0.24.2
	github.com/decred/dcrd/crypto/blake256 v1.0.1 // indirect
	github.com/golang/snappy v0.0.5-0.20220116011046-fa5810519dcb // indirect
	github.com/kkdai/bstream v1.0.0
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

go 1.22.1
