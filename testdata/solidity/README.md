# Solidity E2E fixtures

The legacy EVM tests load these authentic compiled artifacts directly. The loader
checks the source SHA-256, contract name and compiler profile; it never compiles
or downloads anything during a test run.

Compiler: solc 0.8.35+commit.47b9dedd. EVM paris, optimizer enabled, 200 runs.
To regenerate offline, run from sat20wallet/sdk (replace Counter as needed):

```sh
go run ./e2e/cmd/solidity-compile --contract Counter ../../satoshinet/testdata/solidity/Counter.sol > ../../satoshinet/testdata/solidity/Counter.json
```

Network contract E2E already has its own source-hash-checked compiled fixtures in
integration/contract_e2e/solidity_network_fixtures_test.go. Those tests also have
no runtime compiler fallback.
