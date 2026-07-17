package dkvs

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func TestDeleteWatermarkAdvancesWithoutActiveRecord(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	original := signedPersonalRecordWithPath(t, priv, "watermark", 1, "value", 0)
	if _, err := idx.PutLocal(original); err != nil {
		t.Fatal(err)
	}
	firstDelete := signedPersonalRecordWithPath(t, priv, "watermark", 2, "", FlagTombstone)
	if updated, err := idx.PutLocal(firstDelete); err != nil || !updated {
		t.Fatalf("first delete updated=%v err=%v", updated, err)
	}
	laterDelete := signedPersonalRecordWithPath(t, priv, "watermark", 5, "", FlagTombstone)
	if updated, err := idx.PutRemote(laterDelete); err != nil || !updated {
		t.Fatalf("later delete updated=%v err=%v", updated, err)
	}
	stale := signedPersonalRecordWithPath(t, priv, "watermark", 3, "stale", 0)
	if _, err := idx.PutRemote(stale); !errors.Is(err, ErrStaleRecord) {
		t.Fatalf("stale replay err=%v", err)
	}
	fresh := signedPersonalRecordWithPath(t, priv, "watermark", 6, "fresh", 0)
	if updated, err := idx.PutLocal(fresh); err != nil || !updated {
		t.Fatalf("fresh put updated=%v err=%v", updated, err)
	}
}

func TestDeleteCommandCompactionKeepsReplayWatermark(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	original := signedPersonalRecordWithPath(t, priv, "compact", 1, "value", 0)
	if _, err := idx.PutLocal(original); err != nil {
		t.Fatal(err)
	}
	command := signedPersonalRecordWithPath(t, priv, "compact", 4, "", FlagTombstone)
	if _, err := idx.PutLocal(command); err != nil {
		t.Fatal(err)
	}

	idx.mutex.Lock()
	state, err := idx.getDeleteStateRaw(command.Key)
	if err != nil {
		idx.mutex.Unlock()
		t.Fatal(err)
	}
	state.RelayUntil = 1
	encoded, err := encodeDeleteState(state)
	if err == nil {
		err = idx.db.Write(deleteStateDBKey(command.Key), encoded)
	}
	idx.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	compacted, err := idx.CompactDeleteCommands()
	if err != nil || compacted != 1 {
		t.Fatalf("compacted=%d err=%v", compacted, err)
	}
	if _, err := idx.GetForSync(command.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("compacted command remains available: %v", err)
	}
	stale := signedPersonalRecordWithPath(t, priv, "compact", 3, "stale", 0)
	if _, err := idx.PutRemote(stale); !errors.Is(err, ErrStaleRecord) {
		t.Fatalf("compacted watermark did not reject replay: %v", err)
	}
}
