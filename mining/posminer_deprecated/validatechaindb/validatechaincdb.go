// Package kv defines a bolt-db, key-value store implementation
// of the Database interface defined by a Prysm beacon node.
package validatechaindb

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"time"

	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
	bolt "go.etcd.io/bbolt"
)

// var _ iface.Database = (*ValidateChainStore)(nil)

const (
	// NumOfValidatorEntries is the size of the validator cache entries.
	// we expect to hold a max of 200K validators, so setting it to 2 million (10x the capacity).
	NumOfValidatorEntries = 1 << 21
	// ValidatorEntryMaxCost is set to ~64Mb to allow 200K validators entries to be cached.
	ValidatorEntryMaxCost = 1 << 26
	// BeaconNodeDbDirName is the name of the directory containing the beacon node database.
	ValidateDbDirName = "validatechaindata"
	// DatabaseFileName is the name of the beacon node database.
	DatabaseFileName = "validatechain.db"

	boltAllocSize = 8 * 1024 * 1024
	// The size of hash length in bytes
	hashLength = 32

	// Specifies the initial mmap size of bolt.
	mmapSize = 128 * 1024 * 1024
)

// IoConfig defines the shared io parameters.
type IoConfig struct {
	ReadWritePermissions        os.FileMode
	ReadWriteExecutePermissions os.FileMode
	BoltTimeout                 time.Duration
}

var defaultIoConfig = &IoConfig{
	ReadWritePermissions:        0600,            //-rw------- Read and Write permissions for user
	ReadWriteExecutePermissions: 0700,            //-rwx------ Read Write and Execute (traverse) permissions for user
	BoltTimeout:                 1 * time.Second, // 1 second for the bolt DB to timeout.
}

var defaultWindowsIoConfig = &IoConfig{
	ReadWritePermissions:        0666,
	ReadWriteExecutePermissions: 0777,
	BoltTimeout:                 1 * time.Second,
}

// BeaconIoConfig returns the current io config for
// the beacon chain.
func DBIoConfig() *IoConfig {
	if runtime.GOOS == "windows" {
		return defaultWindowsIoConfig
	}
	return defaultIoConfig
}

var (
	vcBlocksBucket    = []byte("vcblocks")    // 所有validateChain的区块数据
	epBlocksBucket    = []byte("epblocks")    // 记录epoch投票投票数据的区块数据
	vcStateBucket     = []byte("vcstate")     // validateChain链的状态
	vcBlockHashBucket = []byte("vcblockhash") // validateChain块对应的Hash值
	// stateSummaryBucket  = []byte("state-summary")
	// chainMetadataBucket = []byte("chain-metadata")
	// checkpointBucket    = []byte("check-point")
	// powchainBucket      = []byte("powchain")
)

// var (
// 	// Metrics for the validator cache.
// 	validatorEntryCacheHit = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "validator_entry_cache_hit_total",
// 		Help: "The total number of cache hits on the validator entry cache.",
// 	})
// 	validatorEntryCacheMiss = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "validator_entry_cache_miss_total",
// 		Help: "The total number of cache misses on the validator entry cache.",
// 	})
// 	validatorEntryCacheDelete = promauto.NewCounter(prometheus.CounterOpts{
// 		Name: "validator_entry_cache_delete_total",
// 		Help: "The total number of cache deletes on the validator entry cache.",
// 	})
// 	stateReadingTime = promauto.NewSummary(prometheus.SummaryOpts{
// 		Name: "db_beacon_state_reading_milliseconds",
// 		Help: "Milliseconds it takes to read a beacon state from the DB",
// 	})
// 	stateSavingTime = promauto.NewSummary(prometheus.SummaryOpts{
// 		Name: "db_beacon_state_saving_milliseconds",
// 		Help: "Milliseconds it takes to save a beacon state to the DB",
// 	})
// )

// BlockCacheSize specifies 1000 slots worth of blocks cached, which
// would be approximately 2MB
var BlockCacheSize = int64(1 << 21)

// Store defines an implementation of the Prysm Database interface
// using BoltDB as the underlying persistent kv-store for Ethereum Beacon Nodes.
type ValidateChainStore struct {
	db           *bolt.DB
	databasePath string
	//ctx          context.Context
}

// DbFilePath is the canonical construction of a full
// database file path from the directory path, so that code outside
// this package can find the full path in a consistent way.
func DbFilePath(dirPath string) string {
	return path.Join(dirPath, DatabaseFileName)
}

var Buckets = [][]byte{
	vcBlocksBucket,
	epBlocksBucket,
	vcStateBucket,
	vcBlockHashBucket,
}

// KVStoreOption is a functional option that modifies a kv.Store.
type VCStoreOption func(*ValidateChainStore)

// NewKVStore initializes a new boltDB key-value store at the directory
// path specified, creates the kv-buckets based on the schema, and stores
// an open connection db object as a property of the Store struct.
func NewVCStore(dirPath string, opts ...VCStoreOption) (*ValidateChainStore, error) {
	hasDir := dirExists(dirPath)

	if !hasDir {
		err := os.MkdirAll(dirPath, 0700)
		if err != nil {
			return nil, err
		}
	}

	datafile := DbFilePath(dirPath)
	utils.Log.Tracef("Opening Bolt DB:path = %s", datafile)
	boltDB, err := bolt.Open(
		datafile,
		DBIoConfig().ReadWritePermissions,
		&bolt.Options{
			Timeout:         1 * time.Second,
			InitialMmapSize: mmapSize,
		},
	)
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, errors.New("cannot obtain database lock, database may be in use by another process")
		}
		return nil, err
	}
	boltDB.AllocSize = boltAllocSize
	// blockCache, err := ristretto.NewCache(&ristretto.Config{
	// 	NumCounters: 1000,           // number of keys to track frequency of (1000).
	// 	MaxCost:     BlockCacheSize, // maximum cost of cache (1000 Blocks).
	// 	BufferItems: 64,             // number of keys per Get buffer.
	// })
	// if err != nil {
	// 	return nil, err
	// }

	// validatorCache, err := ristretto.NewCache(&ristretto.Config{
	// 	NumCounters: NumOfValidatorEntries, // number of entries in cache (2 Million).
	// 	MaxCost:     ValidatorEntryMaxCost, // maximum size of the cache (64Mb)
	// 	BufferItems: 64,                    // number of keys per Get buffer.
	// })
	// if err != nil {
	// 	return nil, err
	// }

	vcStore := &ValidateChainStore{
		db:           boltDB,
		databasePath: dirPath,
		// blockCache:          blockCache,
		// validatorEntryCache: validatorCache,
		// stateSummaryCache:   newStateSummaryCache(),
		//ctx: ctx,
	}
	for _, o := range opts {
		if o != nil {
			o(vcStore)
		}
	}
	if err := vcStore.db.Update(func(tx *bolt.Tx) error {
		return createBuckets(tx, Buckets...)
	}); err != nil {
		return nil, err
	}
	// if err = prometheus.Register(createBoltCollector(kv.db)); err != nil {
	// 	return nil, err
	// }
	// // Setup the type of block storage used depending on whether or not this is a fresh database.
	// if err := kv.setupBlockStorageType(ctx); err != nil {
	// 	return nil, err
	// }

	return vcStore, nil
}

// ClearDB removes the previously stored database in the data directory.
func (s *ValidateChainStore) ClearDB() error {
	if err := s.Close(); err != nil {
		return fmt.Errorf("failed to close db: %w", err)
	}
	if _, err := os.Stat(s.databasePath); os.IsNotExist(err) {
		return nil
	}
	if err := os.Remove(path.Join(s.databasePath, DatabaseFileName)); err != nil {
		return errors.New("could not remove database file")
	}
	return nil
}

// Close closes the underlying BoltDB database.
func (s *ValidateChainStore) Close() error {

	return s.db.Close()
}

// DatabasePath at which this database writes files.
func (s *ValidateChainStore) DatabasePath() string {
	return s.databasePath
}

func createBuckets(tx *bolt.Tx, buckets ...[]byte) error {
	for _, bucket := range buckets {
		if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
			return err
		}
	}
	return nil
}

func dirExists(name string) bool {
	if _, err := os.Stat(name); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}
