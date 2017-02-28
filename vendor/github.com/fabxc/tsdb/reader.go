package tsdb

import (
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fabxc/tsdb/chunks"
	"github.com/fabxc/tsdb/labels"
	"github.com/pkg/errors"
)

// ChunkReader provides reading access of serialized time series data.
type ChunkReader interface {
	// Chunk returns the series data chunk with the given reference.
	Chunk(ref uint64) (chunks.Chunk, error)

	// Close releases all underlying resources of the reader.
	Close() error
}

// chunkReader implements a SeriesReader for a serialized byte stream
// of series data.
type chunkReader struct {
	// The underlying bytes holding the encoded series data.
	bs [][]byte

	// Closers for resources behind the byte slices.
	cs []io.Closer
}

// newChunkReader returns a new chunkReader based on mmaped files found in dir.
func newChunkReader(dir string) (*chunkReader, error) {
	files, err := sequenceFiles(dir, "")
	if err != nil {
		return nil, err
	}
	var cr chunkReader

	for _, fn := range files {
		f, err := openMmapFile(fn)
		if err != nil {
			return nil, errors.Wrapf(err, "mmap files")
		}
		cr.cs = append(cr.cs, f)
		cr.bs = append(cr.bs, f.b)
	}

	for i, b := range cr.bs {
		if len(b) < 4 {
			return nil, errors.Wrapf(errInvalidSize, "validate magic in segment %d", i)
		}
		// Verify magic number.
		if m := binary.BigEndian.Uint32(b[:4]); m != MagicSeries {
			return nil, fmt.Errorf("invalid magic number %x", m)
		}
	}
	return &cr, nil
}

func (s *chunkReader) Close() error {
	return closeAll(s.cs...)
}

func (s *chunkReader) Chunk(ref uint64) (chunks.Chunk, error) {
	var (
		seq = int(ref >> 32)
		off = int((ref << 32) >> 32)
	)
	if seq >= len(s.bs) {
		return nil, errors.Errorf("reference sequence %d out of range", seq)
	}
	b := s.bs[seq]

	if int(off) >= len(b) {
		return nil, errors.Errorf("offset %d beyond data size %d", off, len(b))
	}
	b = b[off:]

	l, n := binary.Uvarint(b)
	if n < 0 {
		return nil, fmt.Errorf("reading chunk length failed")
	}
	b = b[n:]
	enc := chunks.Encoding(b[0])

	c, err := chunks.FromData(enc, b[1:1+l])
	if err != nil {
		return nil, err
	}
	return c, nil
}

// IndexReader provides reading access of serialized index data.
type IndexReader interface {
	// LabelValues returns the possible label values
	LabelValues(names ...string) (StringTuples, error)

	// Postings returns the postings list iterator for the label pair.
	Postings(name, value string) (Postings, error)

	// Series returns the series for the given reference.
	Series(ref uint32) (labels.Labels, []ChunkMeta, error)

	// LabelIndices returns the label pairs for which indices exist.
	LabelIndices() ([][]string, error)

	// Close released the underlying resources of the reader.
	Close() error
}

// StringTuples provides access to a sorted list of string tuples.
type StringTuples interface {
	// Total number of tuples in the list.
	Len() int
	// At returns the tuple at position i.
	At(i int) ([]string, error)
}

type indexReader struct {
	// The underlying byte slice holding the encoded series data.
	b []byte

	// Close that releases the underlying resources of the byte slice.
	c io.Closer

	// Cached hashmaps of section offsets.
	labels   map[string]uint32
	postings map[string]uint32
}

var (
	errInvalidSize = fmt.Errorf("invalid size")
	errInvalidFlag = fmt.Errorf("invalid flag")
)

// newIndexReader returns a new indexReader on the given directory.
func newIndexReader(dir string) (*indexReader, error) {
	f, err := openMmapFile(filepath.Join(dir, "index"))
	if err != nil {
		return nil, err
	}
	r := &indexReader{b: f.b, c: f}

	// Verify magic number.
	if len(f.b) < 4 {
		return nil, errors.Wrap(errInvalidSize, "index header")
	}
	if m := binary.BigEndian.Uint32(r.b[:4]); m != MagicIndex {
		return nil, errors.Errorf("invalid magic number %x", m)
	}

	// The last two 4 bytes hold the pointers to the hashmaps.
	loff := binary.BigEndian.Uint32(r.b[len(r.b)-8 : len(r.b)-4])
	poff := binary.BigEndian.Uint32(r.b[len(r.b)-4:])

	flag, b, err := r.section(loff)
	if err != nil {
		return nil, errors.Wrapf(err, "label index hashmap section at %d", loff)
	}
	if r.labels, err = readHashmap(flag, b); err != nil {
		return nil, errors.Wrap(err, "read label index hashmap")
	}
	flag, b, err = r.section(poff)
	if err != nil {
		return nil, errors.Wrapf(err, "postings hashmap section at %d", loff)
	}
	if r.postings, err = readHashmap(flag, b); err != nil {
		return nil, errors.Wrap(err, "read postings hashmap")
	}

	return r, nil
}

func readHashmap(flag byte, b []byte) (map[string]uint32, error) {
	if flag != flagStd {
		return nil, errInvalidFlag
	}
	h := make(map[string]uint32, 512)

	for len(b) > 0 {
		l, n := binary.Uvarint(b)
		if n < 1 {
			return nil, errors.Wrap(errInvalidSize, "read key length")
		}
		b = b[n:]

		if len(b) < int(l) {
			return nil, errors.Wrap(errInvalidSize, "read key")
		}
		s := string(b[:l])
		b = b[l:]

		o, n := binary.Uvarint(b)
		if n < 1 {
			return nil, errors.Wrap(errInvalidSize, "read offset value")
		}
		b = b[n:]

		h[s] = uint32(o)
	}

	return h, nil
}

func (r *indexReader) Close() error {
	return r.c.Close()
}

func (r *indexReader) section(o uint32) (byte, []byte, error) {
	b := r.b[o:]

	if len(b) < 5 {
		return 0, nil, errors.Wrap(errInvalidSize, "read header")
	}

	flag := b[0]
	l := binary.BigEndian.Uint32(b[1:5])

	b = b[5:]

	// b must have the given length plus 4 bytes for the CRC32 checksum.
	if len(b) < int(l)+4 {
		return 0, nil, errors.Wrap(errInvalidSize, "section content")
	}
	return flag, b[:l], nil
}

func (r *indexReader) lookupSymbol(o uint32) (string, error) {
	if int(o) > len(r.b) {
		return "", errors.Errorf("invalid symbol offset %d", o)
	}
	l, n := binary.Uvarint(r.b[o:])
	if n < 0 {
		return "", errors.New("reading symbol length failed")
	}

	end := int(o) + n + int(l)
	if end > len(r.b) {
		return "", errors.New("invalid length")
	}
	b := r.b[int(o)+n : end]

	return yoloString(b), nil
}

func (r *indexReader) LabelValues(names ...string) (StringTuples, error) {
	key := strings.Join(names, string(sep))
	off, ok := r.labels[key]
	if !ok {
		return nil, fmt.Errorf("label index doesn't exist")
	}

	flag, b, err := r.section(off)
	if err != nil {
		return nil, errors.Wrapf(err, "section at %d", off)
	}
	if flag != flagStd {
		return nil, errInvalidFlag
	}
	l, n := binary.Uvarint(b)
	if n < 1 {
		return nil, errors.Wrap(errInvalidSize, "read label index size")
	}

	st := &serializedStringTuples{
		l:      int(l),
		b:      b[n:],
		lookup: r.lookupSymbol,
	}
	return st, nil
}

func (r *indexReader) LabelIndices() ([][]string, error) {
	res := [][]string{}

	for s := range r.labels {
		res = append(res, strings.Split(s, string(sep)))
	}
	return res, nil
}

func (r *indexReader) Series(ref uint32) (labels.Labels, []ChunkMeta, error) {
	k, n := binary.Uvarint(r.b[ref:])
	if n < 1 {
		return nil, nil, errors.Wrap(errInvalidSize, "number of labels")
	}

	b := r.b[int(ref)+n:]
	lbls := make(labels.Labels, 0, k)

	for i := 0; i < 2*int(k); i += 2 {
		o, m := binary.Uvarint(b)
		if m < 1 {
			return nil, nil, errors.Wrap(errInvalidSize, "symbol offset")
		}
		n, err := r.lookupSymbol(uint32(o))
		if err != nil {
			return nil, nil, errors.Wrap(err, "symbol lookup")
		}
		b = b[m:]

		o, m = binary.Uvarint(b)
		if m < 1 {
			return nil, nil, errors.Wrap(errInvalidSize, "symbol offset")
		}
		v, err := r.lookupSymbol(uint32(o))
		if err != nil {
			return nil, nil, errors.Wrap(err, "symbol lookup")
		}
		b = b[m:]

		lbls = append(lbls, labels.Label{
			Name:  n,
			Value: v,
		})
	}

	// Read the chunks meta data.
	k, n = binary.Uvarint(b)
	if n < 1 {
		return nil, nil, errors.Wrap(errInvalidSize, "number of chunks")
	}

	b = b[n:]
	chunks := make([]ChunkMeta, 0, k)

	for i := 0; i < int(k); i++ {
		firstTime, n := binary.Varint(b)
		if n < 1 {
			return nil, nil, errors.Wrap(errInvalidSize, "first time")
		}
		b = b[n:]

		lastTime, n := binary.Varint(b)
		if n < 1 {
			return nil, nil, errors.Wrap(errInvalidSize, "last time")
		}
		b = b[n:]

		o, n := binary.Uvarint(b)
		if n < 1 {
			return nil, nil, errors.Wrap(errInvalidSize, "chunk offset")
		}
		b = b[n:]

		chunks = append(chunks, ChunkMeta{
			Ref:     o,
			MinTime: firstTime,
			MaxTime: lastTime,
		})
	}

	return lbls, chunks, nil
}

func (r *indexReader) Postings(name, value string) (Postings, error) {
	key := name + string(sep) + value

	off, ok := r.postings[key]
	if !ok {
		return nil, ErrNotFound
	}

	flag, b, err := r.section(off)
	if err != nil {
		return nil, errors.Wrapf(err, "section at %d", off)
	}

	if flag != flagStd {
		return nil, errors.Wrapf(errInvalidFlag, "section at %d", off)
	}

	// TODO(fabxc): just read into memory as an intermediate solution.
	// Add iterator over serialized data.
	var l []uint32

	for len(b) > 0 {
		if len(b) < 4 {
			return nil, errors.Wrap(errInvalidSize, "plain postings entry")
		}
		l = append(l, binary.BigEndian.Uint32(b[:4]))

		b = b[4:]
	}

	return &listPostings{list: l, idx: -1}, nil
}

type stringTuples struct {
	l int      // tuple length
	s []string // flattened tuple entries
}

func newStringTuples(s []string, l int) (*stringTuples, error) {
	if len(s)%l != 0 {
		return nil, errors.Wrap(errInvalidSize, "string tuple list")
	}
	return &stringTuples{s: s, l: l}, nil
}

func (t *stringTuples) Len() int                   { return len(t.s) / t.l }
func (t *stringTuples) At(i int) ([]string, error) { return t.s[i : i+t.l], nil }

func (t *stringTuples) Swap(i, j int) {
	c := make([]string, t.l)
	copy(c, t.s[i:i+t.l])

	for k := 0; k < t.l; k++ {
		t.s[i+k] = t.s[j+k]
		t.s[j+k] = c[k]
	}
}

func (t *stringTuples) Less(i, j int) bool {
	for k := 0; k < t.l; k++ {
		d := strings.Compare(t.s[i+k], t.s[j+k])

		if d < 0 {
			return true
		}
		if d > 0 {
			return false
		}
	}
	return false
}

type serializedStringTuples struct {
	l      int
	b      []byte
	lookup func(uint32) (string, error)
}

func (t *serializedStringTuples) Len() int {
	// TODO(fabxc): Cache this?
	return len(t.b) / (4 * t.l)
}

func (t *serializedStringTuples) At(i int) ([]string, error) {
	if len(t.b) < (i+t.l)*4 {
		return nil, errInvalidSize
	}
	res := make([]string, 0, t.l)

	for k := 0; k < t.l; k++ {
		offset := binary.BigEndian.Uint32(t.b[(i+k)*4:])

		s, err := t.lookup(offset)
		if err != nil {
			return nil, errors.Wrap(err, "symbol lookup")
		}
		res = append(res, s)
	}

	return res, nil
}
