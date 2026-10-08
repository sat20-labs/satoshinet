package evmsource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/scanner"
	"time"

	contract "github.com/sat20-labs/satoshinet/contract"
)

// Official 0.8.30 native binaries, from argotorg/solc-bin's release lists.
var compilerHashes = map[string]bool{
	"f3e987dc6ecebd4bd350c48edcbc320b46cf9e3109bd3fc3d88f1acaf4c428f7": true,
	"738dcdc6afddeb505ee4e4ef24f1c1fdba2b8c924e614cbbf5801a5b062dd683": true,
}

var compilerSlot = make(chan struct{}, 1)

type artifact struct {
	ABI      json.RawMessage
	InitCode []byte
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, fmt.Errorf("solc output limit exceeded")
	}
	return w.Buffer.Write(p)
}

func compile(path, source, name string) (artifact, error) {
	select {
	case compilerSlot <- struct{}{}:
		defer func() { <-compilerSlot }()
	default:
		return artifact{}, fmt.Errorf("source compiler is busy")
	}
	var lex scanner.Scanner
	lex.Init(strings.NewReader(source))
	lex.Error = func(*scanner.Scanner, string) {} // Solidity literals differ from Go's.
	for tok := lex.Scan(); tok != scanner.EOF; tok = lex.Scan() {
		if tok == scanner.Ident && lex.TokenText() == "import" {
			return artifact{}, fmt.Errorf("Solidity imports are disabled")
		}
	}
	if path == "" {
		path = "solc"
	}
	path, err := exec.LookPath(path)
	if err != nil {
		return artifact{}, fmt.Errorf("source compiler unavailable: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return artifact{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return artifact{}, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return artifact{}, err
	}
	if !compilerHashes[hex.EncodeToString(hash.Sum(nil))] {
		return artifact{}, fmt.Errorf("solc binary is not an approved 0.8.30 release")
	}
	profile := contract.DefaultEVMCompilerConfig()
	input := map[string]any{
		"language": "Solidity", "sources": map[string]any{"Contract.sol": map[string]string{"content": source}},
		"settings": map[string]any{
			"evmVersion": profile.EVMVersion, "optimizer": profile.Optimizer,
			"metadata":        profile.Metadata,
			"outputSelection": map[string]any{"*": map[string]any{"*": []string{"abi", "evm.bytecode.object"}}},
		},
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return artifact{}, err
	}
	// One isolated native process, no filesystem import callback. Limits are
	// mandatory; unsupported limits fail closed instead of running unbounded.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "satoshinet-source-")
	if err != nil {
		return artifact{}, err
	}
	defer os.RemoveAll(dir)
	memoryLimit := "ulimit -v 524288"
	if runtime.GOOS == "darwin" {
		// Darwin cannot set RLIMIT_AS. Its data limit must also succeed;
		// unsupported platforms fail closed, including current macOS hosts.
		memoryLimit = "ulimit -d 524288"
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `ulimit -t 10 && `+memoryLimit+` && exec "$1" --no-import-callback --standard-json`, "source-compiler", path)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(payload)
	stdout, stderr := &boundedOutput{limit: 8 << 20}, &boundedOutput{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return artifact{}, fmt.Errorf("Solidity compilation failed: %w: %s", err, stderr.String())
	}
	var output struct {
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
			EVM struct {
				Bytecode struct {
					Object string `json:"object"`
				} `json:"bytecode"`
			} `json:"evm"`
		} `json:"contracts"`
		Errors []struct {
			Severity         string `json:"severity"`
			FormattedMessage string `json:"formattedMessage"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return artifact{}, fmt.Errorf("decode solc output: %w", err)
	}
	for _, diagnostic := range output.Errors {
		if diagnostic.Severity == "error" {
			return artifact{}, fmt.Errorf("Solidity compilation: %s", diagnostic.FormattedMessage)
		}
	}
	compiled, ok := output.Contracts["Contract.sol"][name]
	if !ok {
		return artifact{}, fmt.Errorf("compiled contract %q not found", name)
	}
	code, err := hex.DecodeString(compiled.EVM.Bytecode.Object)
	if err != nil || len(code) == 0 {
		return artifact{}, fmt.Errorf("missing init code or unresolved library link")
	}
	return artifact{ABI: compiled.ABI, InitCode: code}, nil
}
