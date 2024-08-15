module github.com/sat20-labs/satoshinet

require (
	github.com/aead/siphash v1.0.1
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792
	github.com/btcsuite/winsvc v1.0.0
	github.com/davecgh/go-spew v1.1.1
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
	github.com/decred/dcrd/lru v1.1.3
	github.com/gorilla/websocket v1.5.3
	github.com/jessevdk/go-flags v1.6.1
	github.com/jrick/logrotate v1.0.0
	github.com/kkdai/bstream v1.0.0
	github.com/stretchr/testify v1.9.0
	github.com/syndtr/goleveldb v1.0.0
	golang.org/x/crypto v0.26.0
	golang.org/x/sys v0.24.0
)

require (
	github.com/decred/dcrd/crypto/blake256 v1.0.1 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	golang.org/x/net v0.28.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// The retract statements below fixes an accidental push of the tags of a btcd
// fork.

go 1.22.1
