// Package nativeproof binds client-side asset proofs to an STP funding output.
// Bitcoin resolvers supply chain facts only; asset identity and amounts are
// derived from the protocol's own consensus validator.
package nativeproof

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
)

const (
	ProtocolRGB11 = "rgb11"
	// MaxProofSize is below the wire witness-item limit. Transaction and block
	// weight limits still apply to the complete anchor.
	MaxProofSize  = 4_000_000
	maxReveals    = 1024
	maxRevealSize = 64
)

var proofMagic = []byte{'S', 'T', 'P', 'N'}

// Proof is transport material, not a validated asset balance. Version 1
// supports only RGB11 fungible assets and canonical disclosed graph seals.
type Proof struct {
	Protocol    string
	Consignment []byte
	Reveals     [][]byte
}

// IsProof identifies the native envelope without accepting its contents.
func IsProof(raw []byte) bool { return bytes.HasPrefix(raw, proofMagic) }

// BuildRGB11 constructs the canonical native envelope. A successful build
// does not authorize mapping: Validate must run against Bitcoin evidence.
func BuildRGB11(raw []byte, reveals [][]byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxProofSize || len(reveals) > maxReveals {
		return nil, fmt.Errorf("native proof material exceeds limits")
	}
	container, err := consignment.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("RGB11 consignment: %w", err)
	}
	canonical, err := consignment.EncodeFile(container)
	if err != nil {
		return nil, err
	}
	ordered := make([][]byte, len(reveals))
	for i, reveal := range reveals {
		if err := checkReveal(reveal); err != nil {
			return nil, err
		}
		ordered[i] = append([]byte(nil), reveal...)
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i], ordered[j]) < 0 })
	var out bytes.Buffer
	out.Write(proofMagic)
	out.Write([]byte{1, 1}) // envelope version, RGB11 protocol selector
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(canonical)))
	out.Write(canonical)
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(ordered)))
	for i, reveal := range ordered {
		if i > 0 && bytes.Equal(ordered[i-1], reveal) {
			return nil, fmt.Errorf("duplicate native seal reveal")
		}
		out.WriteByte(byte(len(reveal)))
		out.Write(reveal)
	}
	if out.Len() > MaxProofSize {
		return nil, fmt.Errorf("native proof exceeds %d bytes", MaxProofSize)
	}
	return out.Bytes(), nil
}

// Decode checks canonical envelope framing and confinement. It does not
// validate the RGB graph or grant current or historical ownership.
func Decode(raw []byte) (*Proof, error) {
	if len(raw) < 12 || len(raw) > MaxProofSize || !IsProof(raw) || raw[4] != 1 || raw[5] != 1 {
		return nil, fmt.Errorf("unsupported or malformed native proof")
	}
	n := int(binary.LittleEndian.Uint32(raw[6:10]))
	if n == 0 || n > len(raw)-12 {
		return nil, fmt.Errorf("invalid native consignment length")
	}
	result := &Proof{Protocol: ProtocolRGB11, Consignment: append([]byte(nil), raw[10:10+n]...)}
	cursor := 10 + n
	count := int(binary.LittleEndian.Uint16(raw[cursor : cursor+2]))
	cursor += 2
	if count > maxReveals {
		return nil, fmt.Errorf("too many native seal reveals")
	}
	for i := 0; i < count; i++ {
		if cursor >= len(raw) {
			return nil, fmt.Errorf("truncated native seal reveal")
		}
		size := int(raw[cursor])
		cursor++
		if size > len(raw)-cursor {
			return nil, fmt.Errorf("truncated native seal reveal")
		}
		reveal := raw[cursor : cursor+size]
		if err := checkReveal(reveal); err != nil {
			return nil, err
		}
		if i > 0 && bytes.Compare(result.Reveals[i-1], reveal) >= 0 {
			return nil, fmt.Errorf("native seal reveals are not strictly ordered")
		}
		result.Reveals = append(result.Reveals, append([]byte(nil), reveal...))
		cursor += size
	}
	if cursor != len(raw) {
		return nil, fmt.Errorf("trailing native proof data")
	}
	return result, nil
}

func checkReveal(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxRevealSize {
		return fmt.Errorf("invalid native seal reveal length")
	}
	seal, err := seals.DecodeGraphBlindSeal(raw)
	if err != nil {
		return fmt.Errorf("invalid native seal reveal: %w", err)
	}
	canonical, err := seal.StrictBytes()
	if err != nil || !bytes.Equal(canonical, raw) {
		return fmt.Errorf("noncanonical native seal reveal")
	}
	return nil
}
