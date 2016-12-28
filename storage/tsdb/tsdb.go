package tsdb

import (
	"unsafe"

	"github.com/fabxc/tsdb"
	tsdbLabels "github.com/fabxc/tsdb/labels"
	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/storage"
)

// adapter implements a storage.Storage around TSDB.
type adapter struct {
	db *tsdb.DB
}

// Open returns a new storage backed by a tsdb database.
func Open(path string) (storage.Storage, error) {
	db, err := tsdb.Open(path, nil, nil)
	if err != nil {
		return nil, err
	}
	return adapter{db: db}, nil
}

func (a adapter) Querier(mint, maxt int64) (storage.Querier, error) {
	// fmt.Println("new querier at", timestamp.Time(mint), timestamp.Time(maxt), maxt-mint)
	return querier{q: a.db.Querier(mint, maxt)}, nil
}

// Appender returns a new appender against the storage.
func (a adapter) Appender() (storage.Appender, error) {
	return appender{a: a.db.Appender()}, nil
}

// Close closes the storage and all its underlying resources.
func (a adapter) Close() error {
	return a.db.Close()
}

type querier struct {
	q tsdb.Querier
}

func (q querier) Select(oms ...*labels.Matcher) storage.SeriesSet {
	ms := make([]tsdbLabels.Matcher, 0, len(oms))

	for _, om := range oms {
		ms = append(ms, convertMatcher(om))
	}

	return seriesSet{set: q.q.Select(ms...)}
}

func (q querier) LabelValues(name string) ([]string, error) { return q.q.LabelValues(name) }
func (q querier) Close() error                              { return q.q.Close() }

type seriesSet struct {
	set tsdb.SeriesSet
}

func (s seriesSet) Next() bool             { return s.set.Next() }
func (s seriesSet) Err() error             { return s.set.Err() }
func (s seriesSet) Series() storage.Series { return series{s: s.set.Series()} }

type series struct {
	s tsdb.Series
}

func (s series) Labels() labels.Labels            { return toLabels(s.s.Labels()) }
func (s series) Iterator() storage.SeriesIterator { return storage.SeriesIterator(s.s.Iterator()) }

type appender struct {
	a tsdb.Appender
}

func (a appender) Add(lset labels.Labels, t int64, v float64) {
	// fmt.Println("add", lset, timestamp.Time(t), v)
	a.a.Add(toTSDBLabels(lset), t, v)
}
func (a appender) Commit() error { return a.a.Commit() }

func convertMatcher(m *labels.Matcher) tsdbLabels.Matcher {
	switch m.Type {
	case labels.MatchEqual:
		return tsdbLabels.NewEqualMatcher(m.Name, m.Value)

	case labels.MatchNotEqual:
		return tsdbLabels.Not(tsdbLabels.NewEqualMatcher(m.Name, m.Value))

	case labels.MatchRegexp:
		res, err := tsdbLabels.NewRegexpMatcher(m.Name, m.Value)
		if err != nil {
			panic(err)
		}
		return res

	case labels.MatchNotRegexp:
		res, err := tsdbLabels.NewRegexpMatcher(m.Name, m.Value)
		if err != nil {
			panic(err)
		}
		return tsdbLabels.Not(res)
	}
	panic("storage.convertMatcher: invalid matcher type")
}

func toTSDBLabels(l labels.Labels) tsdbLabels.Labels {
	return *(*tsdbLabels.Labels)(unsafe.Pointer(&l))
}

func toLabels(l tsdbLabels.Labels) labels.Labels {
	return *(*labels.Labels)(unsafe.Pointer(&l))
}
