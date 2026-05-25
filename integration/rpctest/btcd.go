// Copyright (c) 2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package rpctest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	// compileMtx guards access to the executable path so that the project is
	// only compiled once.
	compileMtx sync.Mutex

	// executablePath is the path to the compiled executable. This is the empty
	// string until btcd is compiled. This should not be accessed directly;
	// instead use the function btcdExecutablePath().
	executablePath string
)

// btcdExecutablePath returns a path to the btcd executable to be used by
// rpctests. To ensure the code tests against the most up-to-date version of
// btcd, this method compiles btcd the first time it is called. After that, the
// generated binary is used for subsequent test harnesses. The executable file
// is not cleaned up, but since it lives at a static path in a temp directory,
// it is not a big deal.
func btcdExecutablePath() (string, error) {
	compileMtx.Lock()
	defer compileMtx.Unlock()

	// If btcd has already been compiled, just use that.
	if len(executablePath) != 0 {
		return executablePath, nil
	}

	testDir, err := baseDir()
	if err != nil {
		return "", err
	}

	// Build btcd and output an executable in a static temp path.
	outputPath := filepath.Join(testDir, "btcd")
	if runtime.GOOS == "windows" {
		outputPath += ".exe"
	}
	walletPath := filepath.Join(testDir, "wallet.so")
	pluginDir, err := walletPluginSourceDir()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(
		"go", "build", "-buildmode=plugin", "-o", walletPath, "main.go",
	)
	cmd.Dir = pluginDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("Failed to build wallet plugin: %v: %s", err, string(output))
	}
	if err := writeRPCTestWalletConfig(testDir); err != nil {
		return "", err
	}
	cmd = exec.Command(
		"go", "build", "-tags=rpctest,wallet_plugin", "-o", outputPath, "github.com/sat20-labs/satoshinet",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("Failed to build btcd: %v: %s", err, string(output))
	}

	// Save executable path so future calls do not recompile.
	executablePath = outputPath
	return executablePath, nil
}

func writeRPCTestWalletConfig(dir string) error {
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
	return os.WriteFile(cfgPath, data, 0o600)
}

func walletPluginSourceDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to locate rpctest source directory")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "sat20wallet", "sdk", "plugin"))
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		return "", err
	}
	return dir, nil
}
