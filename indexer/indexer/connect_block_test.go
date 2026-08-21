package indexer

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type fakeConnectBlockOps struct {
	height       int
	hash         string
	blocks       map[int]*wire.MsgBlock
	failAt       map[int]error
	fetched      []int
	provided     []int
	prepareCount int
	publishCount int
	serviceTip   int
}

func testBlock(prev chainhash.Hash, nonce uint32) *wire.MsgBlock {
	return &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: prev, Nonce: nonce}}
}

func testBlockChain(from, to int, parent chainhash.Hash) map[int]*wire.MsgBlock {
	blocks := make(map[int]*wire.MsgBlock, to-from+1)
	prev := parent
	for height := from; height <= to; height++ {
		block := testBlock(prev, uint32(height))
		blocks[height] = block
		prev = block.BlockHash()
	}
	return blocks
}

func newFakeConnectBlockOps(height int, hash chainhash.Hash, blocks map[int]*wire.MsgBlock) *fakeConnectBlockOps {
	return &fakeConnectBlockOps{
		height:     height,
		hash:       hash.String(),
		blocks:     blocks,
		failAt:     make(map[int]error),
		serviceTip: height,
	}
}

func (f *fakeConnectBlockOps) ops() connectBlockOps {
	return connectBlockOps{
		internalTip: func() (int, string) {
			return f.height, f.hash
		},
		syncBlockAtHeight: func(height, _ int) error {
			if err := f.failAt[height]; err != nil {
				return err
			}
			block := f.blocks[height]
			if block == nil {
				return fmt.Errorf("missing block %d", height)
			}
			if height != f.height+1 || block.Header.PrevBlock.String() != f.hash {
				return fmt.Errorf("non-contiguous block %d", height)
			}
			f.fetched = append(f.fetched, height)
			f.height = height
			f.hash = block.BlockHash().String()
			return nil
		},
		syncBlock: func(block *wire.MsgBlock, height, _ int) error {
			if err := f.failAt[height]; err != nil {
				return err
			}
			if height != f.height+1 || block.Header.PrevBlock.String() != f.hash {
				return fmt.Errorf("non-contiguous provided block %d", height)
			}
			f.provided = append(f.provided, height)
			f.height = height
			f.hash = block.BlockHash().String()
			return nil
		},
		prepareDBBuffer: func() {
			f.prepareCount++
		},
		publish: func(_, _ int) {
			f.publishCount++
			f.serviceTip = f.height
		},
	}
}

func TestConnectBlockBackfillsBeforeProvidedBlock(t *testing.T) {
	var persistedHash chainhash.Hash
	persistedHash[0] = 1
	blocks := testBlockChain(3428, 3448, persistedHash)
	fake := newFakeConnectBlockOps(3427, persistedHash, blocks)

	if err := connectBlock(fake.ops(), blocks[3448], 3448, 3448); err != nil {
		t.Fatal(err)
	}
	if fake.height != 3448 || fake.serviceTip != 3448 {
		t.Fatalf("tips compiling=%d service=%d", fake.height, fake.serviceTip)
	}
	if got, want := fake.fetched, heightRange(3428, 3447); !reflect.DeepEqual(got, want) {
		t.Fatalf("fetched=%v want=%v", got, want)
	}
	if !reflect.DeepEqual(fake.provided, []int{3448}) {
		t.Fatalf("provided=%v", fake.provided)
	}
	if fake.prepareCount != 1 || fake.publishCount != 1 {
		t.Fatalf("prepare=%d publish=%d", fake.prepareCount, fake.publishCount)
	}
}

func TestConnectBlockCatchUpWithoutProvidedBlock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start int
	}{
		{name: "startup catch-up", start: 3427},
		{name: "one block behind", start: 3447},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var startHash chainhash.Hash
			startHash[0] = byte(tc.start)
			blocks := testBlockChain(tc.start+1, 3448, startHash)
			fake := newFakeConnectBlockOps(tc.start, startHash, blocks)
			if err := connectBlock(fake.ops(), nil, 3448, 3448); err != nil {
				t.Fatal(err)
			}
			if fake.height != 3448 || fake.serviceTip != 3448 {
				t.Fatalf("tips compiling=%d service=%d", fake.height, fake.serviceTip)
			}
			if len(fake.provided) != 0 || fake.fetched[len(fake.fetched)-1] != 3448 {
				t.Fatalf("fetched=%v provided=%v", fake.fetched, fake.provided)
			}
		})
	}
}

func TestConnectBlockContinuousProvidedBlock(t *testing.T) {
	var parent chainhash.Hash
	parent[0] = 7
	block := testBlock(parent, 3448)
	fake := newFakeConnectBlockOps(3447, parent, nil)
	if err := connectBlock(fake.ops(), block, 3448, 3448); err != nil {
		t.Fatal(err)
	}
	if len(fake.fetched) != 0 || !reflect.DeepEqual(fake.provided, []int{3448}) || fake.publishCount != 1 {
		t.Fatalf("fetched=%v provided=%v publish=%d", fake.fetched, fake.provided, fake.publishCount)
	}
}

func TestConnectBlockRepeatedBlockIsIdempotentAndPublishes(t *testing.T) {
	var parent chainhash.Hash
	parent[0] = 8
	block := testBlock(parent, 3448)
	fake := newFakeConnectBlockOps(3448, block.BlockHash(), nil)
	fake.serviceTip = 3427
	if err := connectBlock(fake.ops(), block, 3448, 3448); err != nil {
		t.Fatal(err)
	}
	if len(fake.fetched) != 0 || len(fake.provided) != 0 || fake.publishCount != 1 || fake.serviceTip != 3448 {
		t.Fatalf("fetched=%v provided=%v publish=%d service=%d", fake.fetched, fake.provided, fake.publishCount, fake.serviceTip)
	}
}

func TestConnectBlockRejectsSameHeightDifferentHash(t *testing.T) {
	var parent, other chainhash.Hash
	parent[0], other[0] = 9, 10
	block := testBlock(parent, 3448)
	fake := newFakeConnectBlockOps(3448, other, nil)
	err := connectBlock(fake.ops(), block, 3448, 3448)
	if err == nil || !strings.Contains(err.Error(), "block hash mismatch") {
		t.Fatalf("err=%v", err)
	}
	if fake.publishCount != 0 || len(fake.provided) != 0 {
		t.Fatalf("published=%d provided=%v", fake.publishCount, fake.provided)
	}
}

func TestConnectBlockRejectsParentHashMismatch(t *testing.T) {
	var indexedParent, blockParent chainhash.Hash
	indexedParent[0], blockParent[0] = 11, 12
	block := testBlock(blockParent, 3448)
	fake := newFakeConnectBlockOps(3447, indexedParent, nil)
	err := connectBlock(fake.ops(), block, 3448, 3448)
	if err == nil || !strings.Contains(err.Error(), "parent hash mismatch") {
		t.Fatalf("err=%v", err)
	}
	if fake.publishCount != 0 || len(fake.provided) != 0 {
		t.Fatalf("published=%d provided=%v", fake.publishCount, fake.provided)
	}
}

func TestConnectBlockFailureDoesNotPublishAndRetryResumes(t *testing.T) {
	var persistedHash chainhash.Hash
	persistedHash[0] = 13
	blocks := testBlockChain(3428, 3448, persistedHash)
	fake := newFakeConnectBlockOps(3427, persistedHash, blocks)
	fake.failAt[3430] = errors.New("temporary fetch failure")

	err := connectBlock(fake.ops(), blocks[3448], 3448, 3448)
	if err == nil || !strings.Contains(err.Error(), "temporary fetch failure") {
		t.Fatalf("err=%v", err)
	}
	if fake.height != 3429 || fake.publishCount != 0 || fake.serviceTip != 3427 {
		t.Fatalf("after failure compiling=%d publish=%d service=%d", fake.height, fake.publishCount, fake.serviceTip)
	}

	delete(fake.failAt, 3430)
	if err := connectBlock(fake.ops(), blocks[3448], 3448, 3448); err != nil {
		t.Fatal(err)
	}
	if fake.height != 3448 || fake.serviceTip != 3448 || fake.publishCount != 1 {
		t.Fatalf("after retry compiling=%d service=%d publish=%d", fake.height, fake.serviceTip, fake.publishCount)
	}
	if countHeight(fake.fetched, 3428) != 1 || countHeight(fake.fetched, 3429) != 1 {
		t.Fatalf("already processed blocks repeated: %v", fake.fetched)
	}
}

func heightRange(from, to int) []int {
	result := make([]int, 0, to-from+1)
	for height := from; height <= to; height++ {
		result = append(result, height)
	}
	return result
}

func countHeight(heights []int, target int) int {
	count := 0
	for _, height := range heights {
		if height == target {
			count++
		}
	}
	return count
}

func TestEnsureInternalTipActivelyRepairs(t *testing.T) {
	var parent chainhash.Hash
	parent[0] = 21
	blocks := testBlockChain(3428, 3448, parent)
	fake := newFakeConnectBlockOps(3427, parent, blocks)
	target := blocks[3448].BlockHash()
	if err := ensureInternalTip(fake.ops(), 3448, &target, 3448); err != nil {
		t.Fatal(err)
	}
	if fake.height != 3448 || fake.hash != target.String() || fake.publishCount != 1 {
		t.Fatalf("height=%d hash=%s publish=%d", fake.height, fake.hash, fake.publishCount)
	}
	if got, want := fake.fetched, heightRange(3428, 3448); !reflect.DeepEqual(got, want) {
		t.Fatalf("fetched=%v want=%v", got, want)
	}
}

func TestEnsureInternalTipRejectsWrongCanonicalHash(t *testing.T) {
	var parent, wrong chainhash.Hash
	parent[0], wrong[0] = 22, 99
	blocks := testBlockChain(3448, 3448, parent)
	fake := newFakeConnectBlockOps(3447, parent, blocks)
	err := ensureInternalTip(fake.ops(), 3448, &wrong, 3448)
	if err == nil || !strings.Contains(err.Error(), "repaired internal tip mismatch") {
		t.Fatalf("err=%v", err)
	}
}

func TestEnsureInternalTipRejectsAheadTip(t *testing.T) {
	var current, target chainhash.Hash
	current[0], target[0] = 23, 24
	fake := newFakeConnectBlockOps(3449, current, nil)
	err := ensureInternalTip(fake.ops(), 3448, &target, 3449)
	if err == nil || !strings.Contains(err.Error(), "ahead of readiness target") {
		t.Fatalf("err=%v", err)
	}
	if fake.publishCount != 0 || len(fake.fetched) != 0 {
		t.Fatalf("unexpected repair work publish=%d fetched=%v", fake.publishCount, fake.fetched)
	}
}

func TestEnsureInternalTipFailureCanRetry(t *testing.T) {
	var parent chainhash.Hash
	parent[0] = 25
	blocks := testBlockChain(3428, 3448, parent)
	fake := newFakeConnectBlockOps(3427, parent, blocks)
	fake.failAt[3430] = errors.New("temporary fetch failure")
	target := blocks[3448].BlockHash()
	if err := ensureInternalTip(fake.ops(), 3448, &target, 3448); err == nil || !strings.Contains(err.Error(), "temporary fetch failure") {
		t.Fatalf("first repair err=%v", err)
	}
	if fake.height != 3429 || fake.publishCount != 0 {
		t.Fatalf("failed repair height=%d publish=%d", fake.height, fake.publishCount)
	}
	delete(fake.failAt, 3430)
	if err := ensureInternalTip(fake.ops(), 3448, &target, 3448); err != nil {
		t.Fatal(err)
	}
	if fake.height != 3448 || fake.publishCount != 1 {
		t.Fatalf("retry height=%d publish=%d", fake.height, fake.publishCount)
	}
}
