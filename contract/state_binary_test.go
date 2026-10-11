package contract

import (
	"bytes"
	"crypto/sha256"
	"errors"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"io"
	"math"
	"testing"
)

func TestCompactManagedBalanceRoundTripAndCanonicalOrder(t *testing.T) {
	asset := func(ticker string, amount int64) wire.AssetInfo {
		return wire.AssetInfo{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: ticker}, Amount: *scommon.NewDecimal(amount, 6), BindingSat: 2}
	}
	a := ManagedBalance{Value: 100, Assets: wire.TxAssets{asset("zeta", 3), asset("zero", 0), asset("alpha", 2)}}
	b := ManagedBalance{Value: 100, Assets: wire.TxAssets{asset("alpha", 2), asset("zeta", 3)}}
	raw, err := a.MarshalBinary()
	require.NoError(t, err)
	other, err := b.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, other, raw)
	var restored ManagedBalance
	require.NoError(t, restored.UnmarshalBinary(raw))
	require.Equal(t, b, restored)
	restored.Assets[0].Amount.Value.SetInt64(0)
	require.Equal(t, "2", a.Assets[2].Amount.String())
	for _, bad := range [][]byte{nil, []byte(`{"value":0}`), raw[:len(raw)-1], append(bytes.Clone(raw), 0)} {
		require.Error(t, restored.UnmarshalBinary(bad))
	}
	a.Assets[0].Amount.Precision = 64
	_, err = a.MarshalBinary()
	require.Error(t, err)
}

func TestCompactStateStreamingMatchesBuffered(t *testing.T) {
	write := func(e *StateEncoder) {
		e.Text("root fields")
		e.U64(math.MaxUint64)
		e.I64(math.MinInt64)
		e.Bool(true)
		e.Bool(false)
		e.Blob([]byte{0, 1, 255})
		e.Decimal(nil)
		e.Decimal(scommon.NewDecimalWithScale(-12345, 3))
		WriteManagedBalance(e, ManagedBalance{Value: 7})
	}
	buffered := NewStateEncoder("")
	write(buffered)
	raw, err := buffered.Data()
	require.NoError(t, err)
	hash := sha256.New()
	streamed := NewStateEncoderTo(hash)
	write(streamed)
	require.NoError(t, streamed.Err)
	want := sha256.Sum256(raw)
	require.Equal(t, want[:], hash.Sum(nil))
	require.Zero(t, streamed.Buffer.Len())
	_, err = streamed.Data()
	require.Error(t, err, "streaming must not expose a partial buffered state")
}

type failingStateWriter struct {
	err   error
	calls int
}

func (w *failingStateWriter) Write(p []byte) (int, error) {
	w.calls++
	return 0, w.err
}

func TestCompactStateStreamingPreservesWriteFailure(t *testing.T) {
	for _, expected := range []error{errors.New("write failure"), io.ErrShortWrite} {
		writer := &failingStateWriter{err: expected}
		if expected == io.ErrShortWrite {
			writer.err = nil
		}
		e := NewStateEncoderTo(writer)
		e.U64(7)
		e.Text("must not write")
		e.Decimal(scommon.NewDefaultDecimal(1))
		require.ErrorIs(t, e.Err, expected)
		require.Equal(t, 1, writer.calls)
	}
}
func TestCompactStateRejectsNonCanonicalAndBoundedInput(t *testing.T) {
	for _, raw := range [][]byte{{0x80, 0}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}} {
		d := NewStateDecoder(raw, "")
		d.U64()
		require.Error(t, d.End())
	}
	d := NewStateDecoder([]byte{2}, "")
	d.Bool()
	require.Error(t, d.End())
	d = NewStateDecoder([]byte{100}, "")
	require.Nil(t, d.Blob())
	require.Error(t, d.End())
	for _, raw := range [][]byte{{3}, {1, 64, 0}, {2, 0, 0}, {1, 0, 1, 0}} {
		d := NewStateDecoder(raw, "")
		d.Decimal()
		require.Error(t, d.End())
	}
}
