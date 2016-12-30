// Copyright 2015 The Prometheus Authors
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

package web

import (
	"net/http"
	"sort"

	"github.com/gogo/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/model"

	"github.com/prometheus/prometheus/pkg/labels"
	"github.com/prometheus/prometheus/pkg/timestamp"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
)

var (
	federationErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "prometheus_web_federation_errors_total",
		Help: "Total number of errors that occurred while sending federation responses.",
	})
)

func (h *Handler) federation(w http.ResponseWriter, req *http.Request) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()

	req.ParseForm()

	var matcherSets [][]*labels.Matcher
	for _, s := range req.Form["match[]"] {
		matchers, err := promql.ParseMetricSelector(s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		matcherSets = append(matcherSets, matchers)
	}

	var (
		mint   = timestamp.FromTime(h.now().Time().Add(-promql.StalenessDelta))
		maxt   = timestamp.FromTime(h.now().Time())
		format = expfmt.Negotiate(req.Header)
		enc    = expfmt.NewEncoder(w, format)
	)
	w.Header().Set("Content-Type", string(format))

	q, err := h.storage.Querier(mint, maxt)
	if err != nil {
		federationErrors.Inc()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer q.Close()

	// TODO(fabxc): expose merge functionality in storage interface.
	// We just concatenate results for all sets of matchers, which may produce
	// duplicated results.
	vec := make(promql.Vector, 0, 8000)

	for _, mset := range matcherSets {
		series := q.Select(mset...)
		for series.Next() {
			s := series.Series()
			// TODO(fabxc): allow fast path for most recent sample either
			// in the storage itself or caching layer in Prometheus.
			it := storage.NewBuffer(s.Iterator(), int64(promql.StalenessDelta/1e6))

			var t int64
			var v float64

			ok := it.Seek(maxt)
			if ok {
				t, v = it.Values()
			} else {
				t, v, ok = it.PeekBack()
				if !ok {
					continue
				}
			}

			vec = append(vec, promql.Sample{
				Metric: s.Labels(),
				Point:  promql.Point{T: t, V: v},
			})
		}
		if series.Err() != nil {
			federationErrors.Inc()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	sort.Sort(byName(vec))

	var (
		lastMetricName string
		protMetricFam  *dto.MetricFamily
	)
	for _, s := range vec {
		nameSeen := false
		globalUsed := map[string]struct{}{}
		protMetric := &dto.Metric{
			Untyped: &dto.Untyped{},
		}

		for _, l := range s.Metric {
			if l.Value == "" {
				// No value means unset. Never consider those labels.
				// This is also important to protect against nameless metrics.
				continue
			}
			if l.Name == labels.MetricName {
				nameSeen = true
				if l.Value == lastMetricName {
					// We already have the name in the current MetricFamily,
					// and we ignore nameless metrics.
					continue
				}
				// Need to start a new MetricFamily. Ship off the old one (if any) before
				// creating the new one.
				if protMetricFam != nil {
					if err := enc.Encode(protMetricFam); err != nil {
						federationErrors.Inc()
						log.With("err", err).Error("federation failed")
						return
					}
				}
				protMetricFam = &dto.MetricFamily{
					Type: dto.MetricType_UNTYPED.Enum(),
					Name: proto.String(l.Value),
				}
				lastMetricName = l.Value
				continue
			}
			protMetric.Label = append(protMetric.Label, &dto.LabelPair{
				Name:  proto.String(l.Name),
				Value: proto.String(l.Value),
			})
			if _, ok := h.externalLabels[model.LabelName(l.Name)]; ok {
				globalUsed[l.Name] = struct{}{}
			}
		}
		if !nameSeen {
			log.With("metric", s.Metric).Warn("Ignoring nameless metric during federation.")
			continue
		}
		// Attach global labels if they do not exist yet.
		for ln, lv := range h.externalLabels {
			if _, ok := globalUsed[string(ln)]; !ok {
				protMetric.Label = append(protMetric.Label, &dto.LabelPair{
					Name:  proto.String(string(ln)),
					Value: proto.String(string(lv)),
				})
			}
		}

		protMetric.TimestampMs = proto.Int64(s.T)
		protMetric.Untyped.Value = proto.Float64(s.V)

		protMetricFam.Metric = append(protMetricFam.Metric, protMetric)
	}
	// Still have to ship off the last MetricFamily, if any.
	if protMetricFam != nil {
		if err := enc.Encode(protMetricFam); err != nil {
			federationErrors.Inc()
			log.With("err", err).Error("federation failed")
		}
	}
}

// byName makes a model.Vector sortable by metric name.
type byName promql.Vector

func (vec byName) Len() int      { return len(vec) }
func (vec byName) Swap(i, j int) { vec[i], vec[j] = vec[j], vec[i] }

func (vec byName) Less(i, j int) bool {
	ni := vec[i].Metric.Get(labels.MetricName)
	nj := vec[j].Metric.Get(labels.MetricName)
	return ni < nj
}
