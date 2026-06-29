package cpuminer

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
)

const (
	BTCLuckyBackendLocalTemplate = "local-template"
	BTCLuckyBackendRPCTemplate   = "rpc-template"
	BTCLuckyBackendPeerTemplate  = "peer-template"

	defaultBTCLuckyJobTTL          = 120 * time.Second
	defaultBTCLuckyRefreshInterval = 60 * time.Second
)

// BTCLuckyMinerConfig contains the optional Bitcoin lucky mining settings.
type BTCLuckyMinerConfig struct {
	Enabled      bool
	Backend      string
	RewardAddr   string
	MinerID      string
	Jobs         string
	ReserveCores int
	LowPriority  bool
	Network      string
	JobTTL       time.Duration
}

// BTCLuckyTemplateServiceConfig contains the Bitcoin Core template service
// settings used by core nodes and local lucky miners.
type BTCLuckyTemplateServiceConfig struct {
	Enabled         bool
	Backend         string
	RPCConnect      string
	RPCUser         string
	RPCPass         string
	RPCDisableTLS   bool
	Network         string
	RefreshInterval time.Duration
	JobTTL          time.Duration
	CacheLimit      int
	FoundBlocksFile string
}

// Normalize fills conservative defaults without enabling any optional module.
func (c *BTCLuckyMinerConfig) Normalize() {
	if c.Backend == "" {
		c.Backend = BTCLuckyBackendPeerTemplate
	}
	if c.Jobs == "" {
		c.Jobs = "1"
	}
	if c.Network == "" {
		c.Network = "mainnet"
	}
	if c.JobTTL <= 0 {
		c.JobTTL = defaultBTCLuckyJobTTL
	}
}

// Normalize fills conservative defaults without enabling any optional module.
func (c *BTCLuckyTemplateServiceConfig) Normalize() {
	if c.Backend == "" {
		c.Backend = "bitcoin-core"
	}
	if c.RPCConnect == "" {
		c.RPCConnect = "127.0.0.1:8332"
	}
	if c.Network == "" {
		c.Network = "mainnet"
	}
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = defaultBTCLuckyRefreshInterval
	}
	if c.JobTTL <= 0 {
		c.JobTTL = defaultBTCLuckyJobTTL
	}
	if c.CacheLimit <= 0 {
		c.CacheLimit = 16
	}
}

func ResolveJobCount(jobs string, reserveCores int) (int, error) {
	jobs = strings.TrimSpace(strings.ToLower(jobs))
	if jobs == "" {
		jobs = "1"
	}
	if jobs == "auto" {
		n := runtime.NumCPU() - reserveCores
		if n < 1 {
			n = 1
		}
		return n, nil
	}

	n, err := strconv.Atoi(jobs)
	if err != nil {
		return 0, fmt.Errorf("invalid btc lucky mining jobs %q", jobs)
	}
	if n < 1 {
		return 0, fmt.Errorf("btc lucky mining jobs must be positive")
	}
	return n, nil
}

func BTCChainParams(network string) (*btcchaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "main", "mainnet":
		return &btcchaincfg.MainNetParams, nil
	case "testnet", "testnet4", "test":
		return &btcchaincfg.TestNet4Params, nil
	case "regtest", "regression":
		return &btcchaincfg.RegressionNetParams, nil
	case "simnet", "sim":
		return &btcchaincfg.SimNetParams, nil
	default:
		return nil, fmt.Errorf("unsupported btc lucky mining network %q", network)
	}
}
