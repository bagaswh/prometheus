// Copyright 2020 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package storage

import (
	"math"
	"sort"

	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/prometheus/prometheus/tsdb/tsdbutil"
)

type SeriesEntry struct {
	Lset             labels.Labels
	SampleIteratorFn func() chunkenc.Iterator
}

func (s *SeriesEntry) Labels() labels.Labels       { return s.Lset }
func (s *SeriesEntry) Iterator() chunkenc.Iterator { return s.SampleIteratorFn() }

type ChunkSeriesEntry struct {
	Lset            labels.Labels
	ChunkIteratorFn func() chunks.Iterator
}

func (s *ChunkSeriesEntry) Labels() labels.Labels     { return s.Lset }
func (s *ChunkSeriesEntry) Iterator() chunks.Iterator { return s.ChunkIteratorFn() }

// NewListSeries returns series entry with iterator that allows to iterate over provided samples.
func NewListSeries(lset labels.Labels, s []tsdbutil.Sample) *SeriesEntry {
	return &SeriesEntry{
		Lset: lset,
		SampleIteratorFn: func() chunkenc.Iterator {
			return NewListSeriesIterator(samples(s))
		},
	}
}

// NewListChunkSeriesFromSamples returns chunk series entry that allows to iterate over provided samples.
// NOTE: It uses inefficient chunks encoding implementation, not caring about chunk size.
func NewListChunkSeriesFromSamples(lset labels.Labels, samples ...[]tsdbutil.Sample) *ChunkSeriesEntry {
	return &ChunkSeriesEntry{
		Lset: lset,
		ChunkIteratorFn: func() chunks.Iterator {
			chks := make([]chunks.Meta, 0, len(samples))
			for _, s := range samples {
				chks = append(chks, tsdbutil.ChunkFromSamples(s))
			}
			return NewListChunkSeriesIterator(chks...)
		},
	}
}

type listSeriesIterator struct {
	samples Samples
	idx     int
}

type samples []tsdbutil.Sample

func (s samples) Get(i int) tsdbutil.Sample { return s[i] }
func (s samples) Len() int                  { return len(s) }

// Samples interface allows to work on arrays of types that are compatible with tsdbutil.Sample.
type Samples interface {
	Get(i int) tsdbutil.Sample
	Len() int
}

// NewListSeriesIterator returns listSeriesIterator that allows to iterate over provided samples.
func NewListSeriesIterator(samples Samples) chunkenc.Iterator {
	return &listSeriesIterator{samples: samples, idx: -1}
}

func (it *listSeriesIterator) At() (int64, float64) {
	s := it.samples.Get(it.idx)
	return s.T(), s.V()
}

// AtHistogram always returns (0, histogram.Histogram{}) because there is no
// support for histogram values yet.
func (it *listSeriesIterator) AtHistogram() (int64, histogram.Histogram) {
	return 0, histogram.Histogram{}
}

func (it *listSeriesIterator) ChunkEncoding() chunkenc.Encoding {
	return chunkenc.EncXOR
}

func (it *listSeriesIterator) Next() bool {
	it.idx++
	return it.idx < it.samples.Len()
}

func (it *listSeriesIterator) Seek(t int64) bool {
	if it.idx == -1 {
		it.idx = 0
	}
	// Do binary search between current position and end.
	it.idx = sort.Search(it.samples.Len()-it.idx, func(i int) bool {
		s := it.samples.Get(i + it.idx)
		return s.T() >= t
	})

	return it.idx < it.samples.Len()
}

func (it *listSeriesIterator) Err() error { return nil }

type listChunkSeriesIterator struct {
	chks []chunks.Meta
	idx  int
}

// NewListChunkSeriesIterator returns listChunkSeriesIterator that allows to iterate over provided chunks.
func NewListChunkSeriesIterator(chks ...chunks.Meta) chunks.Iterator {
	return &listChunkSeriesIterator{chks: chks, idx: -1}
}

func (it *listChunkSeriesIterator) At() chunks.Meta {
	return it.chks[it.idx]
}

func (it *listChunkSeriesIterator) Next() bool {
	it.idx++
	return it.idx < len(it.chks)
}

func (it *listChunkSeriesIterator) Err() error { return nil }

type chunkSetToSeriesSet struct {
	ChunkSeriesSet

	chkIterErr       error
	sameSeriesChunks []Series
}

// NewSeriesSetFromChunkSeriesSet converts ChunkSeriesSet to SeriesSet by decoding chunks one by one.
func NewSeriesSetFromChunkSeriesSet(chk ChunkSeriesSet) SeriesSet {
	return &chunkSetToSeriesSet{ChunkSeriesSet: chk}
}

func (c *chunkSetToSeriesSet) Next() bool {
	if c.Err() != nil || !c.ChunkSeriesSet.Next() {
		return false
	}

	iter := c.ChunkSeriesSet.At().Iterator()
	c.sameSeriesChunks = c.sameSeriesChunks[:0]

	for iter.Next() {
		c.sameSeriesChunks = append(
			c.sameSeriesChunks,
			newChunkToSeriesDecoder(c.ChunkSeriesSet.At().Labels(), iter.At()),
		)
	}

	if iter.Err() != nil {
		c.chkIterErr = iter.Err()
		return false
	}
	return true
}

func (c *chunkSetToSeriesSet) At() Series {
	// Series composed of same chunks for the same series.
	return ChainedSeriesMerge(c.sameSeriesChunks...)
}

func (c *chunkSetToSeriesSet) Err() error {
	if c.chkIterErr != nil {
		return c.chkIterErr
	}
	return c.ChunkSeriesSet.Err()
}

func newChunkToSeriesDecoder(labels labels.Labels, chk chunks.Meta) Series {
	return &SeriesEntry{
		Lset: labels,
		SampleIteratorFn: func() chunkenc.Iterator {
			// TODO(bwplotka): Can we provide any chunkenc buffer?
			return chk.Chunk.Iterator(nil)
		},
	}
}

type seriesSetToChunkSet struct {
	SeriesSet
}

// NewSeriesSetToChunkSet converts SeriesSet to ChunkSeriesSet by encoding chunks from samples.
func NewSeriesSetToChunkSet(chk SeriesSet) ChunkSeriesSet {
	return &seriesSetToChunkSet{SeriesSet: chk}
}

func (c *seriesSetToChunkSet) Next() bool {
	if c.Err() != nil || !c.SeriesSet.Next() {
		return false
	}
	return true
}

func (c *seriesSetToChunkSet) At() ChunkSeries {
	return NewSeriesToChunkEncoder(c.SeriesSet.At())
}

func (c *seriesSetToChunkSet) Err() error {
	return c.SeriesSet.Err()
}

type seriesToChunkEncoder struct {
	Series
}

const seriesToChunkEncoderSplit = 120

// NewSeriesToChunkEncoder encodes samples to chunks with 120 samples limit.
func NewSeriesToChunkEncoder(series Series) ChunkSeries {
	return &seriesToChunkEncoder{series}
}

func (s *seriesToChunkEncoder) Iterator() chunks.Iterator {
	chk := chunkenc.NewXORChunk()
	app, err := chk.Appender()
	if err != nil {
		return errChunksIterator{err: err}
	}
	mint := int64(math.MaxInt64)
	maxt := int64(math.MinInt64)

	chks := []chunks.Meta{}

	i := 0
	seriesIter := s.Series.Iterator()
	for seriesIter.Next() {
		// Create a new chunk if too many samples in the current one.
		if i >= seriesToChunkEncoderSplit {
			chks = append(chks, chunks.Meta{
				MinTime: mint,
				MaxTime: maxt,
				Chunk:   chk,
			})
			chk = chunkenc.NewXORChunk()
			app, err = chk.Appender()
			if err != nil {
				return errChunksIterator{err: err}
			}
			mint = int64(math.MaxInt64)
			// maxt is immediately overwritten below which is why setting it here won't make a difference.
			i = 0
		}

		t, v := seriesIter.At()
		app.Append(t, v)

		maxt = t
		if mint == math.MaxInt64 {
			mint = t
		}
		i++
	}
	if err := seriesIter.Err(); err != nil {
		return errChunksIterator{err: err}
	}

	chks = append(chks, chunks.Meta{
		MinTime: mint,
		MaxTime: maxt,
		Chunk:   chk,
	})

	return NewListChunkSeriesIterator(chks...)
}

type errChunksIterator struct {
	err error
}

func (e errChunksIterator) At() chunks.Meta { return chunks.Meta{} }
func (e errChunksIterator) Next() bool      { return false }
func (e errChunksIterator) Err() error      { return e.err }

// ExpandSamples iterates over all samples in the iterator, buffering all in slice.
// Optionally it takes samples constructor, useful when you want to compare sample slices with different
// sample implementations. if nil, sample type from this package will be used.
func ExpandSamples(iter chunkenc.Iterator, newSampleFn func(t int64, v float64, h *histogram.Histogram) tsdbutil.Sample) ([]tsdbutil.Sample, error) {
	if newSampleFn == nil {
		newSampleFn = func(t int64, v float64, h *histogram.Histogram) tsdbutil.Sample { return sample{t, v, h} }
	}

	var result []tsdbutil.Sample
	if iter.ChunkEncoding() == chunkenc.EncHistogram {
		for iter.Next() {
			t, h := iter.AtHistogram()
			result = append(result, newSampleFn(t, 0, &h))
		}
	} else {
		for iter.Next() {
			t, v := iter.At()
			// NaNs can't be compared normally, so substitute for another value.
			if math.IsNaN(v) {
				v = -42
			}
			result = append(result, newSampleFn(t, v, nil))
		}
	}
	return result, iter.Err()
}

// ExpandChunks iterates over all chunks in the iterator, buffering all in slice.
func ExpandChunks(iter chunks.Iterator) ([]chunks.Meta, error) {
	var result []chunks.Meta
	for iter.Next() {
		result = append(result, iter.At())
	}
	return result, iter.Err()
}
