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

package targetgroup

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v2"
	"github.com/prometheus/common/model"
	
	"github.com/prometheus/prometheus/util/testutil"
)

func TestTargetGroupStrictJsonUnmarshal(t *testing.T) {
	tests := []struct {
		json            string
		expectedReply   error
		expectedTargets []model.LabelSet
	}{
		{
			json: `	{"labels": {},"targets": []}`,
			expectedReply:   nil,
			expectedTargets: []model.LabelSet{},
		},
		{
			json: `	{"labels": {},"targets": ["localhost:9090","localhost:9091"]}`,
			expectedReply: nil,
			expectedTargets: []model.LabelSet{
				model.LabelSet{"__address__": "localhost:9090"},
				model.LabelSet{"__address__": "localhost:9091"}},
		},
		{
			json: `	{"label": {},"targets": []}`,
			expectedReply:   errors.New("json: unknown field \"label\""),
			expectedTargets: nil,
		},
		{
			json: `	{"labels": {},"target": []}`,
			expectedReply:   errors.New("json: unknown field \"target\""),
			expectedTargets: nil,
		},
	}

	for _, test := range tests {
		tg := Group{}
		actual := tg.UnmarshalJSON([]byte(test.json))
		testutil.Equals(t, test.expectedReply, actual)
		testutil.Equals(t, test.expectedTargets, tg.Targets)
	}

}

func TestTargetGroupYamlMarshal(t *testing.T) {
	marshal := func(g interface{}) []byte {
		d, err := yaml.Marshal(g)
		if err != nil {
			panic(err)
		}
		return d
	}

	tests := []struct {
		expectedYaml  string
		expectetedErr error
		group         Group
	}{
		{
			// labels should be omitted if empty.
			group:         Group{},
			expectedYaml:  "targets: []\n",
			expectetedErr: nil,
		},
		{
			// targets only exposes addresses.
			group: Group{Targets: []model.LabelSet{
				model.LabelSet{"__address__": "localhost:9090"},
				model.LabelSet{"__address__": "localhost:9091"}},
				Labels: model.LabelSet{"foo": "bar", "bar": "baz"}},
			expectedYaml:  "targets:\n- localhost:9090\n- localhost:9091\nlabels:\n  bar: baz\n  foo: bar\n",
			expectetedErr: nil,
		},
	}

	for _, test := range tests {
		actual, err := test.group.MarshalYAML()
		testutil.Equals(t, test.expectetedErr, err)
		testutil.Equals(t, test.expectedYaml, string(marshal(actual)))
	}
}

func TestTargetGroupYamlUnmarshal(t *testing.T) {
	unmarshal := func(d []byte) func(interface{}) error {
		return func(o interface{}) error {
			return yaml.Unmarshal(d, o)
		}
	}
	tests := []struct {
		yaml                   string
		expectedTargets        []model.LabelSet
		expectedNumberOfLabels int
		expectedReply          error
	}{
		{
			// empty target group.
			yaml:                   "labels:\ntargets:\n",
			expectedNumberOfLabels: 0,
			expectedTargets:        []model.LabelSet{},
			expectedReply:          nil,
		},
		{
			// brackets syntax.
			yaml:                   "labels:\n  my:  label\ntargets:\n  ['localhost:9090', 'localhost:9191']",
			expectedNumberOfLabels: 1,
			expectedReply:          nil,
			expectedTargets: []model.LabelSet{
				model.LabelSet{"__address__": "localhost:9090"},
				model.LabelSet{"__address__": "localhost:9191"}}},
		{
			// incorrect syntax.
			yaml:                   "labels:\ntargets:\n  'localhost:9090'",
			expectedNumberOfLabels: 0,
			expectedReply:          &yaml.TypeError{Errors: []string{"line 3: cannot unmarshal !!str `localho...` into []string"}},
		},
	}

	for _, test := range tests {
		tg := Group{}
		actual := tg.UnmarshalYAML(unmarshal([]byte(test.yaml)))
		testutil.Equals(t, test.expectedReply, actual)
		testutil.Equals(t, test.expectedNumberOfLabels, len(tg.Labels))
		testutil.Equals(t, test.expectedTargets, tg.Targets)
	}

}

func TestString(t *testing.T) {
	// String() should return only the source, regardless of other attributes.
	group1 :=
		Group{Targets: []model.LabelSet{
			model.LabelSet{"__address__": "localhost:9090"},
			model.LabelSet{"__address__": "localhost:9091"}},
			Source: "<source>",
			Labels: model.LabelSet{"foo": "bar", "bar": "baz"}}
	group2 :=
		Group{Targets: []model.LabelSet{},
			Source: "<source>",
			Labels: model.LabelSet{}}
	testutil.Equals(t, "<source>", group1.String())
	testutil.Equals(t, "<source>", group2.String())
	testutil.Equals(t, group1.String(), group2.String())
}
