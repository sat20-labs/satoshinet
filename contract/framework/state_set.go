package framework

import (
	"crypto/sha256"
	"sort"

	contract "github.com/sat20-labs/satoshinet/contract"
)

type EngineState interface {
	Root() [32]byte
	Snapshot() any
}

type RuntimeStore = EngineState

type RootEngineState struct {
	StateRoot     [32]byte
	StateSnapshot any
}

func (s RootEngineState) Root() [32]byte { return s.StateRoot }
func (s RootEngineState) Snapshot() any { return s.StateSnapshot }

type StateSet struct {
	Engines map[ModuleType]EngineState
	Roots   map[ModuleType][32]byte
}

func NewStateSet() *StateSet {
	return &StateSet{Engines: make(map[ModuleType]EngineState), Roots: make(map[ModuleType][32]byte)}
}

func (s *StateSet) SetRoot(module ModuleType, root [32]byte) {
	if s.Roots == nil {
		s.Roots = make(map[ModuleType][32]byte)
	}
	s.Roots[module] = root
}

func (s *StateSet) SetEngine(module ModuleType, engine EngineState) {
	if s.Engines == nil {
		s.Engines = make(map[ModuleType]EngineState)
	}
	s.Engines[module] = engine
	if engine != nil {
		s.SetRoot(module, engine.Root())
	}
}

func (s *StateSet) Root(module ModuleType) [32]byte {
	if s == nil {
		return [32]byte{}
	}
	if root, ok := s.Roots[module]; ok {
		return root
	}
	if engine := s.Engines[module]; engine != nil {
		return engine.Root()
	}
	return [32]byte{}
}

func (s *StateSet) CombinedRoot() [32]byte {
	// Preserve the established three-slot commitment for the built-ins.
	// Additional registered modules are committed in numeric type order;
	// adding a runtime can no longer silently omit its state from the root.
	types := map[ModuleType]bool{ModuleTemplate: true, ModuleEVM: true, ModuleAgent: true}
	if s != nil {
		for typ := range s.Roots {
			types[typ] = true
		}
		for typ := range s.Engines {
			types[typ] = true
		}
	}
	if len(types) == 3 {
		return contract.CombineStateRoots(s.Root(ModuleTemplate), s.Root(ModuleEVM), s.Root(ModuleAgent))
	}
	ordered := make([]int, 0, len(types))
	for typ := range types {
		ordered = append(ordered, int(typ))
	}
	sort.Ints(ordered)
	h := sha256.New()
	h.Write([]byte("SATOSHINET:MODULE_STATE_ROOT\x00"))
	for _, typ := range ordered {
		h.Write([]byte{byte(typ)})
		root := s.Root(ModuleType(typ))
		h.Write(root[:])
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

func StateSetFromRoots(roots map[ModuleType][32]byte) *StateSet {
	state := NewStateSet()
	for module, root := range roots {
		state.SetRoot(module, root)
	}
	return state
}
