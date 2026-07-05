package indexer

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestPruneExpiredDKVSOnBlock(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })

	idx := dkvs.New(database, dkvs.Config{AllowFreeLocal: true, CurrentHeight: func() uint64 { return 1 }})
	mgr := &IndexerMgr{dkvsIndexer: idx}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedDKVSTestPersonalRecord(t, priv, 1)
	record.ExpiryHeight = 2
	signDKVSTestRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}

	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks - 1})
	if pruned, err := idx.PruneExpiredAt(uint64(dkvsPruneIntervalBlocks - 1)); err != nil || pruned != 1 {
		t.Fatalf("manual prune before interval pruned=%d err=%v", pruned, err)
	}

	record = signedDKVSTestPersonalRecord(t, priv, 2)
	record.ExpiryHeight = 2
	signDKVSTestRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks})
	if mgr.lastDKVSPruneHeight != dkvsPruneIntervalBlocks {
		t.Fatalf("last prune height=%d", mgr.lastDKVSPruneHeight)
	}
	if _, err := idx.GetByHash(dkvs.RecordHash(record)); err != dkvs.ErrRecordNotFound {
		t.Fatalf("expired hash after auto prune err=%v", err)
	}
	if pruned, err := idx.PruneExpiredAt(uint64(dkvsPruneIntervalBlocks)); err != nil || pruned != 0 {
		t.Fatalf("manual prune after auto pruned=%d err=%v", pruned, err)
	}
	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks})
	if mgr.lastDKVSPruneHeight != dkvsPruneIntervalBlocks {
		t.Fatalf("repeat prune changed height=%d", mgr.lastDKVSPruneHeight)
	}
}

func TestDKVSConfigMergesExternalIntegrations(t *testing.T) {
	mainnetMgr := &IndexerMgr{
		cfg:           &Config{},
		chaincfgParam: &chaincfg.MainNetParams,
	}
	if mainnetMgr.dkvsConfig().AllowFreeLocal {
		t.Fatalf("mainnet dkvs config should not allow free local writes")
	}
	testnetMgr := &IndexerMgr{
		cfg:           &Config{},
		chaincfgParam: &chaincfg.TestNetParams,
	}
	testnetCfg := testnetMgr.dkvsConfig()
	if testnetCfg.AllowFreeLocal {
		t.Fatalf("testnet dkvs config should require default autopay fee proof")
	}
	defaultAutopayVerifier, ok := testnetCfg.FeeVerifier.(dkvs.AutopayFeeVerifier)
	if !ok {
		t.Fatalf("testnet default fee verifier not configured: %T", testnetCfg.FeeVerifier)
	}
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	if defaultAutopayVerifier.Recipient != defaults.AutopayRecipient ||
		defaultAutopayVerifier.FeeAssetName != defaults.AutopayFeeAssetName ||
		defaultAutopayVerifier.FullRecordFeePerBlock != defaults.FullRecordFeePerBlock ||
		defaultAutopayVerifier.AddressParams != &chaincfg.TestNetParams {
		t.Fatalf("testnet default autopay verifier config=%+v defaults=%+v", defaultAutopayVerifier, defaults)
	}

	allowFreeLocal := false
	mgr := &IndexerMgr{
		cfg: &Config{
			DKVS: &DKVSIntegrationConfig{
				Resolver:       dkvs.StaticDIDResolver{},
				FeeVerifier:    dkvs.JSONFeeVerifier{AllowFreeLocal: true},
				SystemVerifier: dkvs.StaticSystemVerifier{},
				MailboxPolicy:  dkvs.MailboxPolicy{MaxMessages: 7},
				BlobPolicy:     dkvs.BlobPolicy{MaxChunks: 9},
				TmpPolicy:      dkvs.TmpPolicy{MaxTTL: 11},
				AllowFreeLocal: &allowFreeLocal,
			},
		},
		chaincfgParam: &chaincfg.TestNetParams,
	}
	cfg := mgr.dkvsConfig()
	if cfg.AllowFreeLocal {
		t.Fatalf("explicit allow free local override not applied")
	}
	if _, ok := cfg.Resolver.(dkvs.StaticDIDResolver); !ok {
		t.Fatalf("resolver not merged: %T", cfg.Resolver)
	}
	if _, ok := cfg.FeeVerifier.(dkvs.JSONFeeVerifier); !ok {
		t.Fatalf("fee verifier not merged: %T", cfg.FeeVerifier)
	}
	if _, ok := cfg.SystemVerifier.(dkvs.StaticSystemVerifier); !ok {
		t.Fatalf("system verifier not merged: %T", cfg.SystemVerifier)
	}
	if cfg.MailboxPolicy.MaxMessages != 7 || cfg.BlobPolicy.MaxChunks != 9 || cfg.TmpPolicy.MaxTTL != 11 {
		t.Fatalf("policies not merged: mailbox=%+v blob=%+v tmp=%+v", cfg.MailboxPolicy, cfg.BlobPolicy, cfg.TmpPolicy)
	}

	httpMgr := &IndexerMgr{
		cfg: &Config{
			DKVS: &DKVSIntegrationConfig{
				ResolverHTTPBaseURL:        "http://127.0.0.1:18080/did",
				ResolverHTTPNamePath:       "/names/",
				ResolverHTTPServicePath:    "/services/",
				FeeVerifierHTTPEndpoint:    "http://127.0.0.1:18081/fee/verify",
				SystemVerifierHTTPEndpoint: "http://127.0.0.1:18082/system/verify",
			},
		},
		chaincfgParam: &chaincfg.MainNetParams,
	}
	httpCfg := httpMgr.dkvsConfig()
	resolver, ok := httpCfg.Resolver.(dkvs.HTTPDIDResolver)
	if !ok {
		t.Fatalf("http resolver not configured: %T", httpCfg.Resolver)
	}
	if resolver.BaseURL != "http://127.0.0.1:18080/did" ||
		resolver.NamePath != "/names/" ||
		resolver.ServicePath != "/services/" {
		t.Fatalf("http resolver config=%+v", resolver)
	}
	feeVerifier, ok := httpCfg.FeeVerifier.(dkvs.HTTPFeeVerifier)
	if !ok {
		t.Fatalf("http fee verifier not configured: %T", httpCfg.FeeVerifier)
	}
	if feeVerifier.Endpoint != "http://127.0.0.1:18081/fee/verify" {
		t.Fatalf("http fee endpoint=%s", feeVerifier.Endpoint)
	}
	systemVerifier, ok := httpCfg.SystemVerifier.(dkvs.HTTPSystemVerifier)
	if !ok {
		t.Fatalf("http system verifier not configured: %T", httpCfg.SystemVerifier)
	}
	if systemVerifier.Endpoint != "http://127.0.0.1:18082/system/verify" {
		t.Fatalf("http system endpoint=%s", systemVerifier.Endpoint)
	}

	l1Mgr := &IndexerMgr{
		cfg: &Config{
			DKVS: &DKVSIntegrationConfig{
				ResolverL1NSBaseURL:     "http://127.0.0.1:18084",
				ResolverL1NSNamePath:    "/ns/name/",
				ResolverL1NSServicePath: "/ns/name/",
			},
		},
		chaincfgParam: &chaincfg.TestNetParams,
	}
	l1Resolver, ok := l1Mgr.dkvsConfig().Resolver.(dkvs.L1NSResolver)
	if !ok {
		t.Fatalf("l1 ns resolver not configured: %T", l1Mgr.dkvsConfig().Resolver)
	}
	if l1Resolver.BaseURL != "http://127.0.0.1:18084" ||
		l1Resolver.NamePath != "/ns/name/" ||
		l1Resolver.ServicePath != "/ns/name/" ||
		l1Resolver.AddressParams != &chaincfg.TestNetParams {
		t.Fatalf("l1 resolver config=%+v", l1Resolver)
	}

	autopayMgr := &IndexerMgr{
		cfg: &Config{
			DKVS: &DKVSIntegrationConfig{
				AutopayFeeRecipient:          "dkvs-fee-recipient",
				AutopayFeeAssetName:          "sat",
				AutopayFullRecordFeePerBlock: "1",
			},
		},
		chaincfgParam: &chaincfg.TestNetParams,
	}
	autopayVerifier, ok := autopayMgr.dkvsConfig().FeeVerifier.(dkvs.AutopayFeeVerifier)
	if !ok {
		t.Fatalf("autopay fee verifier not configured: %T", autopayMgr.dkvsConfig().FeeVerifier)
	}
	if autopayVerifier.Recipient != "dkvs-fee-recipient" ||
		autopayVerifier.FeeAssetName != "sat" ||
		autopayVerifier.FullRecordFeePerBlock != "1" ||
		autopayVerifier.AddressParams != &chaincfg.TestNetParams {
		t.Fatalf("autopay verifier config=%+v", autopayVerifier)
	}

	explicitMgr := &IndexerMgr{
		cfg: &Config{
			DKVS: &DKVSIntegrationConfig{
				Resolver:                   dkvs.StaticDIDResolver{},
				ResolverHTTPBaseURL:        "http://127.0.0.1:18080/did",
				ResolverL1NSBaseURL:        "http://127.0.0.1:18084",
				FeeVerifier:                dkvs.JSONFeeVerifier{AllowFreeLocal: true},
				FeeVerifierHTTPEndpoint:    "http://127.0.0.1:18081/fee/verify",
				SystemVerifier:             dkvs.StaticSystemVerifier{},
				SystemVerifierHTTPEndpoint: "http://127.0.0.1:18082/system/verify",
			},
		},
		chaincfgParam: &chaincfg.MainNetParams,
	}
	explicitCfg := explicitMgr.dkvsConfig()
	if _, ok := explicitCfg.Resolver.(dkvs.StaticDIDResolver); !ok {
		t.Fatalf("explicit resolver not preferred: %T", explicitCfg.Resolver)
	}
	if _, ok := explicitCfg.FeeVerifier.(dkvs.JSONFeeVerifier); !ok {
		t.Fatalf("explicit fee verifier not preferred: %T", explicitCfg.FeeVerifier)
	}
	if _, ok := explicitCfg.SystemVerifier.(dkvs.StaticSystemVerifier); !ok {
		t.Fatalf("explicit system verifier not preferred: %T", explicitCfg.SystemVerifier)
	}

}

func signedDKVSTestPersonalRecord(t *testing.T, priv *btcec.PrivateKey, seq uint64) *wire.DKVSRecord {
	t.Helper()
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := dkvs.NewRecord(key, []byte("value"), priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{
		Seq:          seq,
		TTL:          60_000,
		ExpiryHeight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	signDKVSTestRecord(t, priv, record)
	return record
}

func signDKVSTestRecord(t *testing.T, priv *btcec.PrivateKey, record *wire.DKVSRecord) {
	t.Helper()
	hash := dkvs.SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
}
