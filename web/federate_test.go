// Copyright 2016 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package web

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql"
)

var scenarios = map[string]struct {
	params string
	accept string
	code   int
	body   string
}{
	"empty": {
		params: "",
		code:   200,
		body:   ``,
	},
	"invalid params from the beginning": {
		params: "match[]=-not-a-valid-metric-name",
		code:   400,
		body: `parse error at char 1: vector selector must contain label matchers or metric name
`,
	},
	"invalid params somehwere in the middle": {
		params: "match[]=not-a-valid-metric-name",
		code:   400,
		body: `parse error at char 4: could not parse remaining input "-a-valid-metric"...
`,
	},
	"test_metric1": {
		params: "match[]=test_metric1",
		code:   200,
		body: `# TYPE test_metric1 untyped
test_metric1{foo="bar"} 10000 6000000
test_metric1{foo="boo"} 1 6000000
`,
	},
	"test_metric2": {
		params: "match[]=test_metric2",
		code:   200,
		body: `# TYPE test_metric2 untyped
test_metric2{foo="boo"} 1 6000000
`,
	},
	"test_metric_without_labels": {
		params: "match[]=test_metric_without_labels",
		code:   200,
		body: `# TYPE test_metric_without_labels untyped
test_metric_without_labels 1001 6000000
`,
	},
	"{foo='boo'}": {
		params: "match[]={foo='boo'}",
		code:   200,
		body: `# TYPE test_metric1 untyped
test_metric1{foo="boo"} 1 6000000
# TYPE test_metric2 untyped
test_metric2{foo="boo"} 1 6000000
`,
	},
	"two matchers": {
		params: "match[]=test_metric1&match[]=test_metric2",
		code:   200,
		body: `# TYPE test_metric1 untyped
test_metric1{foo="bar"} 10000 6000000
test_metric1{foo="boo"} 1 6000000
# TYPE test_metric2 untyped
test_metric2{foo="boo"} 1 6000000
`,
	},
	"everything": {
		params: "match[]={__name__=~'.%2b'}", // '%2b' is an URL-encoded '+'.
		code:   200,
		body: `# TYPE test_metric1 untyped
test_metric1{foo="bar"} 10000 6000000
test_metric1{foo="boo"} 1 6000000
# TYPE test_metric2 untyped
test_metric2{foo="boo"} 1 6000000
# TYPE test_metric_without_labels untyped
test_metric_without_labels 1001 6000000
`,
	},
}

func TestFederation(t *testing.T) {
	suite, err := promql.NewTest(t, `
		load 1m
			test_metric1{foo="bar"}    0+100x100
			test_metric1{foo="boo"}    1+0x100
			test_metric2{foo="boo"}    1+0x100
			test_metric_without_labels 1+10x100
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer suite.Close()

	if err := suite.Run(); err != nil {
		t.Fatal(err)
	}

	h := &Handler{
		storage:     suite.Storage(),
		queryEngine: suite.QueryEngine(),
		now:         func() model.Time { return 101 * 60 * 1000 }, // 101min after epoch.
	}

	for name, scenario := range scenarios {
		req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(
			"GET http://example.org/federate?" + scenario.params + " HTTP/1.0\r\n\r\n",
		)))
		if err != nil {
			t.Fatal(err)
		}
		// HTTP/1.0 was used above to avoid needing a Host field. Change it to 1.1 here.
		req.Proto = "HTTP/1.1"
		req.ProtoMinor = 1
		req.Close = false
		// 192.0.2.0/24 is "TEST-NET" in RFC 5737 for use solely in
		// documentation and example source code and should not be
		// used publicly.
		req.RemoteAddr = "192.0.2.1:1234"
		// TODO(beorn7): Once we are completely on Go1.7, replace the lines above by the following:
		// req := httptest.NewRequest("GET", "http://example.org/federate?"+scenario.params, nil)
		res := httptest.NewRecorder()
		h.federation(res, req)
		if got, want := res.Code, scenario.code; got != want {
			t.Errorf("Scenario %q: got code %d, want %d", name, got, want)
		}
		if got, want := normalizeBody(res.Body), scenario.body; got != want {
			t.Errorf("Scenario %q: got body %q, want %q", name, got, want)
		}
	}
}

// normalizeBody sorts the lines within a metric to make it easy to verify the body.
// (Federation is not taking care of sorting within a metric family.)
func normalizeBody(body *bytes.Buffer) string {
	var (
		lines    []string
		lastHash int
	)
	for line, err := body.ReadString('\n'); err == nil; line, err = body.ReadString('\n') {
		if line[0] == '#' && len(lines) > 0 {
			sort.Strings(lines[lastHash+1:])
			lastHash = len(lines)
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		sort.Strings(lines[lastHash+1:])
	}
	return strings.Join(lines, "")
}
