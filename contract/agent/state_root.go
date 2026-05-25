package agent

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

type byteWriter interface {
	Write([]byte) (int, error)
}

func (r *Runtime) StateRoot() [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(r.address.EncodeAddress()))
	writeLengthPrefixed(h, []byte(r.deploy.Subtype))
	writeUint32(h, r.deploy.AgentVersion)
	writeLengthPrefixed(h, []byte(r.deploy.Deployer))
	writeLengthPrefixed(h, r.deploy.Random)
	writeLengthPrefixed(h, r.deploy.ContractContent)
	stateJSON, _ := r.StateJSON()
	writeLengthPrefixed(h, stateJSON)
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func writeLengthPrefixed(buf byteWriter, data []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(data)))
	buf.Write(lenBuf[:])
	buf.Write(data)
}

func writeUint32(buf byteWriter, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func stateRootFromRuntimes(runtimes map[string]*Runtime) [32]byte {
	h := sha256.New()
	keys := make([]string, 0, len(runtimes))
	for key := range runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeLengthPrefixed(h, []byte(key))
		root := runtimes[key].StateRoot()
		writeLengthPrefixed(h, root[:])
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}
