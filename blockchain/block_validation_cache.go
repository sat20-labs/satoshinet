package blockchain

import (
	"container/list"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

const (
	blockValidationCacheEpoch = uint32(1)
	maxRejectedBlocks         = 2048
	rejectedBlockTTL          = 6 * time.Hour
	maxPreparedBlocks         = 64
	preparedBlockTTL          = 5 * time.Minute
)

type rejectedBlockEntry struct {
	err     RuleError
	expires time.Time
	epoch   uint32
	element *list.Element
}

type preparedBlockEntry struct {
	parent  chainhash.Hash
	expires time.Time
	element *list.Element
}

// blockValidationCache keeps only bounded validation metadata. It never owns
// raw block bytes and is intentionally cleared by process restart or an epoch
// bump.
type blockValidationCache struct {
	mu sync.Mutex

	rejected      map[chainhash.Hash]*rejectedBlockEntry
	rejectedOrder *list.List
	prepared      map[chainhash.Hash]*preparedBlockEntry
	preparedOrder *list.List
}

func newBlockValidationCache() *blockValidationCache {
	return &blockValidationCache{
		rejected:      make(map[chainhash.Hash]*rejectedBlockEntry),
		rejectedOrder: list.New(),
		prepared:      make(map[chainhash.Hash]*preparedBlockEntry),
		preparedOrder: list.New(),
	}
}

func (c *blockValidationCache) addRejected(hash chainhash.Hash, ruleErr RuleError) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.rejected[hash]; old != nil {
		old.err = ruleErr
		old.expires = now.Add(rejectedBlockTTL)
		old.epoch = blockValidationCacheEpoch
		c.rejectedOrder.MoveToBack(old.element)
		return
	}
	e := &rejectedBlockEntry{err: ruleErr, expires: now.Add(rejectedBlockTTL), epoch: blockValidationCacheEpoch}
	e.element = c.rejectedOrder.PushBack(hash)
	c.rejected[hash] = e
	for len(c.rejected) > maxRejectedBlocks {
		front := c.rejectedOrder.Front()
		delete(c.rejected, front.Value.(chainhash.Hash))
		c.rejectedOrder.Remove(front)
	}
}

func (c *blockValidationCache) rejectedError(hash chainhash.Hash) (RuleError, bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.rejected[hash]
	if e == nil {
		return RuleError{}, false
	}
	if e.epoch != blockValidationCacheEpoch || !now.Before(e.expires) {
		delete(c.rejected, hash)
		c.rejectedOrder.Remove(e.element)
		return RuleError{}, false
	}
	c.rejectedOrder.MoveToBack(e.element)
	return e.err, true
}

func (c *blockValidationCache) addPrepared(hash, parent chainhash.Hash) []chainhash.Hash {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.prepared[hash]; old != nil {
		old.parent = parent
		old.expires = now.Add(preparedBlockTTL)
		c.preparedOrder.MoveToBack(old.element)
		return nil
	}
	e := &preparedBlockEntry{parent: parent, expires: now.Add(preparedBlockTTL)}
	e.element = c.preparedOrder.PushBack(hash)
	c.prepared[hash] = e
	var evicted []chainhash.Hash
	for len(c.prepared) > maxPreparedBlocks {
		front := c.preparedOrder.Front()
		evictedHash := front.Value.(chainhash.Hash)
		delete(c.prepared, evictedHash)
		c.preparedOrder.Remove(front)
		evicted = append(evicted, evictedHash)
	}
	return evicted
}

func (c *blockValidationCache) takePrepared(hash, parent chainhash.Hash) (bool, bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.prepared[hash]
	if e == nil {
		return false, false
	}
	delete(c.prepared, hash)
	c.preparedOrder.Remove(e.element)
	ready := now.Before(e.expires) && e.parent == parent
	return ready, !ready
}
