// Package evm contains SatoshiNet EVM protocol primitives.
//
// The first implementation batch intentionally keeps these primitives isolated
// from consensus, mempool, and miner code. Integration code should consume this
// package when those paths are wired to the EVM protocol.
package evm
