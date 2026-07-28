//go:build rpctest

package contract_e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	walletRuntimeBuildMu sync.Mutex
	walletRuntimePath    string
)

// contractE2EExecutablePath builds the wallet-enabled node variant required by
// these tests. The generic rpctest harness deliberately builds a node without a
// wallet implementation; tests that require one opt in through customExePath.
func contractE2EExecutablePath(t *testing.T) string {
	t.Helper()

	walletRuntimeBuildMu.Lock()
	defer walletRuntimeBuildMu.Unlock()

	if walletRuntimePath != "" {
		return walletRuntimePath
	}

	satoshinetDir, walletPluginDir := contractE2ESourceDirs(t)
	buildDir, err := os.MkdirTemp("", "satoshinet-contract-e2e-")
	if err != nil {
		t.Fatalf("create contract E2E build directory: %v", err)
	}

	pluginPath := filepath.Join(buildDir, "wallet.so")
	runBuild(t, walletPluginDir,
		"go", "build", "-buildmode=plugin", "-o", pluginPath, "main.go",
	)

	executablePath := filepath.Join(buildDir, "satoshinet")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	runBuild(t, satoshinetDir,
		"go", "build", "-tags=rpctest,wallet_plugin", "-o", executablePath,
		"github.com/sat20-labs/satoshinet",
	)

	writeContractE2EWalletConfig(t, buildDir)
	walletRuntimePath = executablePath
	return walletRuntimePath
}

func contractE2ESourceDirs(t *testing.T) (string, string) {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract E2E source directory")
	}
	satoshinetDir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	walletPluginDir := filepath.Join(filepath.Dir(satoshinetDir), "sat20wallet", "sdk", "plugin")
	if _, err := os.Stat(filepath.Join(walletPluginDir, "main.go")); err != nil {
		t.Fatalf("locate wallet plugin source: %v", err)
	}
	return satoshinetDir, walletPluginDir
}

func runBuild(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build contract E2E runtime: %v: %s", err, output)
	}
}

func writeContractE2EWalletConfig(t *testing.T, dir string) {
	t.Helper()

	cfgPath := filepath.Join(dir, "conf.yaml")
	data := []byte(`env: rpctest
chain: testnet
mode: local
log: info
db: ""
indexer_layer1:
  scheme: http
  host: 127.0.0.1:1
  proxy: testnet
indexer_layer2:
  scheme: http
  host: 127.0.0.1:1
  proxy: testnet
rpc:
  scheme: http
  host: 127.0.0.1:1
  proxy: testnet
wallet:
  mode: local
  password: rpctest
`)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("write contract E2E wallet config: %v", err)
	}
}
