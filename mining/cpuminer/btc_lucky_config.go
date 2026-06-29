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
	Workers      string
	MaxWorkers   int
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
	SubmitBlock     bool
}

// Normalize fills conservative defaults without enabling any optional module.
func (c *BTCLuckyMinerConfig) Normalize() {
	if c.Backend == "" {
		c.Backend = BTCLuckyBackendPeerTemplate
	}
	if c.Workers == "" {
		c.Workers = "auto"
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

func ResolveWorkerCount(workers string, reserveCores, maxWorkers int) (int, error) {
	workers = strings.TrimSpace(strings.ToLower(workers))
	if workers == "" || workers == "auto" {
		n := runtime.NumCPU() - reserveCores
		if n < 1 {
			n = 1
		}
		if maxWorkers > 0 && n > maxWorkers {
			n = maxWorkers
		}
		return n, nil
	}

	n, err := strconv.Atoi(workers)
	if err != nil {
		return 0, fmt.Errorf("invalid btc lucky mining workers %q", workers)
	}
	if n < 1 {
		return 0, fmt.Errorf("btc lucky mining workers must be positive")
	}
	if maxWorkers > 0 && n > maxWorkers {
		n = maxWorkers
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
