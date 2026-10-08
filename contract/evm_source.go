package contract

// Source verification deliberately uses one compiler profile. ABI and init
// code hashes are checked by the node, never accepted as proof on their own.
func DefaultEVMCompilerConfig() EVMCompilerConfig {
	var cfg EVMCompilerConfig
	cfg.SolcVersion = "0.8.30"
	cfg.EVMVersion = "paris"
	cfg.Optimizer.Enabled = true
	cfg.Optimizer.Runs = 200
	cfg.Metadata.BytecodeHash = "none"
	cfg.SingleFileOnly = true
	return cfg
}
