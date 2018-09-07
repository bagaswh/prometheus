// Copyright 2018 The Prometheus Authors
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
package remote

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/pkg/timestamp"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/util/testutil"
	"github.com/prometheus/tsdb"
	"github.com/prometheus/tsdb/labels"
	"github.com/prometheus/tsdb/wal"
)

type writeToMock struct {
	samplesAppended      int
	seriesLabels         map[uint64][]prompb.Label
	seriesSegmentIndexes map[uint64]int
}

func (wtm *writeToMock) Append(s []tsdb.RefSample) bool {
	wtm.samplesAppended += len(s)
	return true
}

func (wtm *writeToMock) StoreSeries(series []tsdb.RefSeries, index int) {
	temp := make(map[uint64][]prompb.Label, len(series))
	for _, s := range series {
		ls := make(model.LabelSet, len(s.Labels))
		for _, label := range s.Labels {
			ls[model.LabelName(label.Name)] = model.LabelValue(label.Value)
		}

		temp[s.Ref] = labelsetToLabelsProto(ls)
	}
	// wtm.seriesMtx.Lock()
	// defer t.seriesMtx.Unlock()
	for ref, labels := range temp {
		wtm.seriesLabels[ref] = labels
		wtm.seriesSegmentIndexes[ref] = index
	}
}

func (wtm *writeToMock) SeriesReset(index int) {
	// Check for series that are in segments older than the checkpoint
	// that were not also present in the checkpoint.
	for k, v := range wtm.seriesSegmentIndexes {
		if v < index {
			delete(wtm.seriesLabels, k)
			delete(wtm.seriesSegmentIndexes, k)
		}
	}
}

func (wtm *writeToMock) Name() string {
	return ""
}

func newWriteToMock() *writeToMock {
	return &writeToMock{
		seriesLabels:         make(map[uint64][]prompb.Label),
		seriesSegmentIndexes: make(map[uint64]int),
	}
}

// we need a way to check the value of the wal watcher records read metrics, the samples and series records
// with these we could write some example segments and checkpoints and then write tests for readSegment/watch
// to see if we get back the write number of series records/samples records/etc., and that we read a whole checkpoint
// on startup and when a new one is created
//
// we could do the same thing for readToEnd, readCheckpoint, readSeriesRecords, etc.
func Test_readToEnd_noCheckpoint(t *testing.T) {
	pageSize := 32 * 1024
	const seriesCount = 10
	const samplesCount = 250

	dir, err := ioutil.TempDir("", "readToEnd_noCheckpoint")
	testutil.Ok(t, err)
	defer os.RemoveAll(dir)
	wdir := path.Join(dir, "wal")
	err = os.Mkdir(wdir, 0777)
	testutil.Ok(t, err)

	w, err := wal.NewSize(nil, nil, wdir, 128*pageSize)
	testutil.Ok(t, err)

	// var input [][]byte
	var recs [][]byte

	enc := tsdb.RecordEncoder{}

	for i := 0; i < seriesCount; i++ {
		series := enc.Series([]tsdb.RefSeries{
			tsdb.RefSeries{
				Ref:    uint64(i),
				Labels: labels.Labels{labels.Label{"__name__", fmt.Sprintf("metric_%d", i)}},
			},
		}, nil)
		recs = append(recs, series)
		for j := 0; j < samplesCount; j++ {
			sample := enc.Samples([]tsdb.RefSample{
				tsdb.RefSample{
					Ref: uint64(j),
					T:   int64(i),
					V:   float64(i),
				},
			}, nil)

			recs = append(recs, sample)

			// Randomly batch up records.
			if rand.Intn(4) < 3 {
				testutil.Ok(t, w.Log(recs...))
				recs = recs[:0]
			}
		}
	}
	testutil.Ok(t, w.Log(recs...))

	first, last, err := w.Segments()
	testutil.Ok(t, err)

	wt := newWriteToMock()
	st := timestamp.FromTime(time.Now())
	watcher := NewWALWatcher(nil, wt, dir, st)
	_, _, err = watcher.readToEnd(wdir, first, last)
	testutil.Ok(t, err)
	testutil.Equals(t, seriesCount, len(wt.seriesLabels))
}

func Test_readToEnd_withCheckpoint(t *testing.T) {
	pageSize := 32 * 1024
	const seriesCount = 10
	const samplesCount = 250

	dir, err := ioutil.TempDir("", "readToEnd_withCheckpoint")
	testutil.Ok(t, err)
	defer os.RemoveAll(dir)

	wdir := path.Join(dir, "wal")
	err = os.Mkdir(wdir, 0777)
	testutil.Ok(t, err)

	os.Create(wal.SegmentName(wdir, 30))

	enc := tsdb.RecordEncoder{}
	w, err := wal.NewSize(nil, nil, wdir, 128*pageSize)
	testutil.Ok(t, err)

	// write to the initial segment then checkpoint
	for i := 0; i < seriesCount*10; i++ {
		ref := i + 100
		series := enc.Series([]tsdb.RefSeries{
			tsdb.RefSeries{
				Ref:    uint64(ref),
				Labels: labels.Labels{labels.Label{"__name__", fmt.Sprintf("metric_%d", i)}},
			},
		}, nil)
		testutil.Ok(t, w.Log(series))

		for j := 0; j < samplesCount*10; j++ {
			inner := rand.Intn(ref + 1)
			sample := enc.Samples([]tsdb.RefSample{
				tsdb.RefSample{
					Ref: uint64(inner),
					T:   int64(i),
					V:   float64(i),
				},
			}, nil)
			testutil.Ok(t, w.Log(sample))
		}
	}
	tsdb.Checkpoint(w, 30, 31, func(x uint64) bool { return true }, 0)
	w.Truncate(32)

	// write more records after checkpointing
	for i := 0; i < seriesCount*10; i++ {
		series := enc.Series([]tsdb.RefSeries{
			tsdb.RefSeries{
				Ref:    uint64(i),
				Labels: labels.Labels{labels.Label{"__name__", fmt.Sprintf("metric_%d", i)}},
			},
		}, nil)
		testutil.Ok(t, w.Log(series))

		for j := 0; j < samplesCount*10; j++ {
			sample := enc.Samples([]tsdb.RefSample{
				tsdb.RefSample{
					Ref: uint64(j),
					T:   int64(i),
					V:   float64(i),
				},
			}, nil)
			testutil.Ok(t, w.Log(sample))
		}
	}

	first, last, err := w.Segments()
	testutil.Ok(t, err)

	wt := newWriteToMock()
	st := timestamp.FromTime(time.Now())
	watcher := NewWALWatcher(nil, wt, dir, st)
	_, _, err = watcher.readToEnd(wdir, first, last)
	testutil.Ok(t, err)
	testutil.Equals(t, seriesCount*10*2, len(wt.seriesLabels))
}

func Test_readCheckpoint(t *testing.T) {
	pageSize := 32 * 1024
	const seriesCount = 10
	const samplesCount = 250

	dir, err := ioutil.TempDir("", "readCheckpoint")
	testutil.Ok(t, err)
	defer os.RemoveAll(dir)

	wdir := path.Join(dir, "wal")
	err = os.Mkdir(wdir, 0777)
	testutil.Ok(t, err)

	os.Create(wal.SegmentName(wdir, 30))

	enc := tsdb.RecordEncoder{}
	w, err := wal.NewSize(nil, nil, wdir, 128*pageSize)
	testutil.Ok(t, err)

	// write to the initial segment then checkpoint
	for i := 0; i < seriesCount*10; i++ {
		ref := i + 100
		series := enc.Series([]tsdb.RefSeries{
			tsdb.RefSeries{
				Ref:    uint64(ref),
				Labels: labels.Labels{labels.Label{"__name__", fmt.Sprintf("metric_%d", i)}},
			},
		}, nil)
		testutil.Ok(t, w.Log(series))

		for j := 0; j < samplesCount*10; j++ {
			inner := rand.Intn(ref + 1)
			sample := enc.Samples([]tsdb.RefSample{
				tsdb.RefSample{
					Ref: uint64(inner),
					T:   int64(i),
					V:   float64(i),
				},
			}, nil)
			testutil.Ok(t, w.Log(sample))
		}
	}
	tsdb.Checkpoint(w, 30, 31, func(x uint64) bool { return true }, 0)
	w.Truncate(32)

	first, last, err := w.Segments()
	testutil.Ok(t, err)

	wt := newWriteToMock()
	st := timestamp.FromTime(time.Now())
	watcher := NewWALWatcher(nil, wt, dir, st)
	_, _, err = watcher.readToEnd(wdir, first, last)
	testutil.Ok(t, err)
	testutil.Equals(t, seriesCount*10, len(wt.seriesLabels))
}

func Test_checkpoint_seriesReset(t *testing.T) {
	pageSize := 32 * 1024
	const seriesCount = 10
	const samplesCount = 250

	dir, err := ioutil.TempDir("", "seriesReset")
	testutil.Ok(t, err)
	defer os.RemoveAll(dir)

	wdir := path.Join(dir, "wal")
	err = os.Mkdir(wdir, 0777)
	testutil.Ok(t, err)

	enc := tsdb.RecordEncoder{}
	w, err := wal.NewSize(nil, nil, wdir, pageSize)
	// w.
	testutil.Ok(t, err)

	// write to the initial segment then checkpoint
	for i := 0; i < seriesCount*10; i++ {
		ref := i + 100
		series := enc.Series([]tsdb.RefSeries{
			tsdb.RefSeries{
				Ref:    uint64(ref),
				Labels: labels.Labels{labels.Label{"__name__", fmt.Sprintf("metric_%d", i)}},
			},
		}, nil)
		testutil.Ok(t, w.Log(series))

		for j := 0; j < samplesCount*10; j++ {
			inner := rand.Intn(ref + 1)
			sample := enc.Samples([]tsdb.RefSample{
				tsdb.RefSample{
					Ref: uint64(inner),
					T:   int64(i),
					V:   float64(i),
				},
			}, nil)
			testutil.Ok(t, w.Log(sample))
		}
	}

	first, last, err := w.Segments()
	testutil.Ok(t, err)

	wt := newWriteToMock()
	st := timestamp.FromTime(time.Now())
	watcher := NewWALWatcher(nil, wt, dir, st)
	_, _, err = watcher.readToEnd(wdir, first, last)
	testutil.Ok(t, err)
	testutil.Equals(t, seriesCount*10, len(wt.seriesLabels))

	// If you modify the checkpoint and truncate segment #'s run the test to see how
	// many series records you end up with and change the last Equals check accordingly
	// or modify the Equals to Assert(len(wt.seriesLabels) < seriesCount*10)
	_, err = tsdb.Checkpoint(w, 50, 200, func(x uint64) bool { return true }, 0)
	testutil.Ok(t, err)
	w.Truncate(200)

	cp, _, err := tsdb.LastCheckpoint(path.Join(dir, "wal"))
	testutil.Ok(t, err)
	err = watcher.readCheckpoint(cp)
	testutil.Ok(t, err)
}

func Test_decodeRecord(t *testing.T) {
	dir, err := ioutil.TempDir("", "decodeRecord")
	testutil.Ok(t, err)
	defer os.RemoveAll(dir)

	wt := newWriteToMock()
	st := timestamp.FromTime(time.Now())
	watcher := NewWALWatcher(nil, wt, dir, st)

	// decode a series record
	enc := tsdb.RecordEncoder{}
	buf := enc.Series([]tsdb.RefSeries{tsdb.RefSeries{Ref: 1234, Labels: labels.Labels{}}}, nil)
	watcher.decodeRecord(buf)
	testutil.Ok(t, err)

	testutil.Equals(t, 1, len(wt.seriesLabels))

	// decode a samples record
	buf = enc.Samples([]tsdb.RefSample{tsdb.RefSample{Ref: 100, T: 1, V: 1.0}, tsdb.RefSample{Ref: 100, T: 2, V: 2.0}}, nil)
	watcher.decodeRecord(buf)
	testutil.Ok(t, err)

	testutil.Equals(t, 2, wt.samplesAppended)
}
