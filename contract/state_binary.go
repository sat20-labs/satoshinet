package contract

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

// StateEncoder writes the fixed schemas used by contract persistence and roots. It has
// no reflection, field names or Go type metadata in the encoded stream.
type StateEncoder struct {
	bytes.Buffer
	Err     error
	output  io.Writer
	scratch [10]byte
}

func NewStateEncoder(header string) *StateEncoder {
	e := &StateEncoder{}
	e.WriteString(header)
	return e
}

// NewStateEncoderTo streams fields directly into a hash or another writer.
func NewStateEncoderTo(output io.Writer) *StateEncoder {
	return &StateEncoder{output: output}
}
func (e *StateEncoder) Write(p []byte) (int, error) {
	if e.Err != nil {
		return 0, e.Err
	}
	if e.output == nil {
		return e.Buffer.Write(p)
	}
	n, err := e.output.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	e.Err = err
	return n, err
}
func (e *StateEncoder) WriteString(s string) (int, error) {
	if e.Err != nil {
		return 0, e.Err
	}
	if e.output == nil {
		return e.Buffer.WriteString(s)
	}
	return e.Write([]byte(s))
}
func (e *StateEncoder) WriteByte(v byte) error {
	e.scratch[0] = v
	_, err := e.Write(e.scratch[:1])
	return err
}
func (e *StateEncoder) U64(v uint64) { n := binary.PutUvarint(e.scratch[:], v); e.Write(e.scratch[:n]) }
func (e *StateEncoder) I64(v int64)  { e.U64(uint64(v<<1) ^ uint64(v>>63)) }
func (e *StateEncoder) Bool(v bool) {
	if v {
		e.WriteByte(1)
	} else {
		e.WriteByte(0)
	}
}
func (e *StateEncoder) Blob(v []byte) { e.U64(uint64(len(v))); e.Write(v) }
func (e *StateEncoder) Text(v string) { e.U64(uint64(len(v))); e.WriteString(v) }
func (e *StateEncoder) Decimal(v *scommon.Decimal) {
	if e.Err != nil {
		return
	}
	if v == nil {
		e.WriteByte(0)
		return
	}
	if err := v.Validate(); err != nil {
		e.Err = err
		return
	}
	if len(v.Value.String()) > scommon.MaxProtocolDecimalTextLength {
		e.Err = fmt.Errorf("decimal value exceeds protocol text limit")
		return
	}
	tag := byte(1)
	if v.Sign() < 0 {
		tag = 2
	}
	e.WriteByte(tag)
	e.U64(uint64(v.Precision))
	e.Blob(v.Value.Bytes())
}
func (e *StateEncoder) Data() ([]byte, error) {
	if e.Err != nil {
		return nil, e.Err
	}
	if e.output != nil {
		return nil, fmt.Errorf("streamed state has no buffered data")
	}
	return e.Bytes(), nil
}

// StateDecoder checks lengths before allocating and rejects noncanonical
// integers, flags and decimals. Failed reads never publish a partial state.
type StateDecoder struct {
	*bytes.Reader
	Err error
}

func NewStateDecoder(data []byte, header string) *StateDecoder {
	d := &StateDecoder{Reader: bytes.NewReader(data)}
	if !bytes.HasPrefix(data, []byte(header)) {
		d.Err = fmt.Errorf("invalid contract state header")
		return d
	}
	d.Seek(int64(len(header)), io.SeekStart)
	return d
}
func (d *StateDecoder) Fail(err error) {
	if d.Err == nil {
		d.Err = err
	}
}
func (d *StateDecoder) U64() uint64 {
	if d.Err != nil {
		return 0
	}
	before := d.Len()
	v, err := binary.ReadUvarint(d.Reader)
	if err != nil {
		d.Fail(err)
		return 0
	}
	var b [10]byte
	if binary.PutUvarint(b[:], v) != before-d.Len() {
		d.Fail(fmt.Errorf("noncanonical state integer"))
		return 0
	}
	return v
}
func (d *StateDecoder) I64() int64 { v := d.U64(); return int64(v>>1) ^ -int64(v&1) }
func (d *StateDecoder) U32() uint32 {
	v := d.U64()
	if v > math.MaxUint32 {
		d.Fail(fmt.Errorf("state uint32 overflow"))
	}
	return uint32(v)
}
func (d *StateDecoder) Int() int {
	v := d.I64()
	if int64(int(v)) != v {
		d.Fail(fmt.Errorf("state int overflow"))
	}
	return int(v)
}
func (d *StateDecoder) Bool() bool {
	if d.Err != nil {
		return false
	}
	b, err := d.ReadByte()
	if err != nil {
		d.Fail(err)
		return false
	}
	if b > 1 {
		d.Fail(fmt.Errorf("invalid state boolean"))
	}
	return b == 1
}
func (d *StateDecoder) Count() int {
	n := d.U64()
	if n > uint64(d.Len()) {
		d.Fail(io.ErrUnexpectedEOF)
		return 0
	}
	return int(n)
}

// CountEntries bounds allocation using the schema's minimum record size,
// without imposing a new business capacity limit.
func (d *StateDecoder) CountEntries(minBytes int) int {
	n := d.U64()
	if minBytes < 1 || n > uint64(d.Len()/minBytes) {
		d.Fail(io.ErrUnexpectedEOF)
		return 0
	}
	return int(n)
}
func (d *StateDecoder) Blob() []byte {
	n := d.Count()
	if d.Err != nil || n == 0 {
		return nil
	}
	b := make([]byte, n)
	_, err := io.ReadFull(d.Reader, b)
	d.Fail(err)
	return b
}
func (d *StateDecoder) Text() string { return string(d.Blob()) }
func (d *StateDecoder) Decimal() *scommon.Decimal {
	if d.Err != nil {
		return nil
	}
	tag, err := d.ReadByte()
	if err != nil {
		d.Fail(err)
		return nil
	}
	if tag == 0 {
		return nil
	}
	if tag > 2 {
		d.Fail(fmt.Errorf("invalid state decimal sign"))
		return nil
	}
	p := d.U64()
	if p > scommon.MAX_PRECISION {
		d.Fail(fmt.Errorf("decimal precision outside protocol range"))
		return nil
	}
	raw := d.Blob()
	if d.Err != nil {
		return nil
	}
	if len(raw) > 0 && raw[0] == 0 || tag == 2 && len(raw) == 0 {
		d.Fail(fmt.Errorf("noncanonical state decimal"))
		return nil
	}
	// 256 decimal text characters need at most 107 magnitude bytes. Bound the
	// magnitude before converting it to a decimal string for the exact check.
	if len(raw) > 107 {
		d.Fail(fmt.Errorf("decimal value exceeds protocol text limit"))
		return nil
	}
	n := new(big.Int).SetBytes(raw)
	if tag == 2 {
		n.Neg(n)
	}
	if len(n.String()) > scommon.MaxProtocolDecimalTextLength {
		d.Fail(fmt.Errorf("decimal value exceeds protocol text limit"))
		return nil
	}
	return &scommon.Decimal{Precision: int(p), Value: n}
}
func (d *StateDecoder) End() error {
	if d.Err != nil {
		return d.Err
	}
	if d.Len() != 0 {
		return fmt.Errorf("trailing contract state bytes")
	}
	return nil
}
func SortedStateKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (d *StateDecoder) MapKey(previous string, index int) string {
	key := d.Text()
	if index > 0 && key <= previous {
		d.Fail(fmt.Errorf("unordered or duplicate state map key"))
	}
	return key
}

func (b ManagedBalance) MarshalBinary() ([]byte, error) {
	e := NewStateEncoder("MB\x01")
	WriteManagedBalance(e, b)
	return e.Data()
}
func (b *ManagedBalance) UnmarshalBinary(data []byte) error {
	d := NewStateDecoder(data, "MB\x01")
	next := readManagedState(d)
	if err := d.End(); err != nil {
		return err
	}
	*b = next
	return nil
}

// WriteManagedBalance shares the deterministic fields with persistence and roots.
func WriteManagedBalance(e *StateEncoder, b ManagedBalance) {
	if e.Err != nil {
		return
	}
	if err := b.Validate(); err != nil {
		e.Err = err
		return
	}
	b = b.Clone()
	b.normalize()
	e.I64(b.Value)
	e.U64(uint64(len(b.Assets)))
	for _, a := range b.Assets {
		e.Text(a.Name.Protocol)
		e.Text(a.Name.Type)
		e.Text(a.Name.Ticker)
		e.Decimal(&a.Amount)
		e.U64(uint64(a.BindingSat))
	}
}
func readManagedState(d *StateDecoder) ManagedBalance {
	b := ManagedBalance{Value: d.I64()}
	count := d.CountEntries(5)
	if count > 0 {
		b.Assets = make(wire.TxAssets, count)
	}
	for i := range b.Assets {
		a := &b.Assets[i]
		a.Name.Protocol = d.Text()
		a.Name.Type = d.Text()
		a.Name.Ticker = d.Text()
		amount := d.Decimal()
		if amount == nil {
			d.Fail(fmt.Errorf("missing managed asset amount"))
			return b
		}
		a.Amount = *amount
		a.BindingSat = d.U32()
	}
	if d.Err == nil {
		d.Fail(b.Validate())
		if d.Err == nil {
			b.normalize()
		}
	}
	return b
}
