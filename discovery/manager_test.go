// Copyright 2016 The Prometheus Authors
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

package discovery

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	yaml "gopkg.in/yaml.v2"
)

// TestDiscoveryManagerSyncCalls checks that the target updates are received in the expected order.
func TestDiscoveryManagerSyncCalls(t *testing.T) {

	// The order by which the updates are send is detirmened by the interval passed to the mock discovery adapter
	// Final targets array is ordered alphabetically by the name of the discoverer.
	// For example discoverer "A" with targets "t2,t3" and discoverer "B" with targets "t1,t2" will result in "t2,t3,t1,t2" after the merge.
	testCases := []struct {
		title           string
		updates         map[string][]update
		expectedTargets [][]*config.TargetGroup
	}{
		{
			title: "Single TP no updates",
			updates: map[string][]update{
				"tp1": {},
			},
			expectedTargets: nil,
		},
		{
			title: "Multips TPs no updates",
			updates: map[string][]update{
				"tp1": {},
				"tp2": {},
				"tp3": {},
			},
			expectedTargets: nil,
		},
		{
			title: "Single TP empty initials",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{},
						interval:     5,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{},
			},
		},
		{
			title: "Multiple TPs empty initials",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{},
						interval:     5,
					},
				},
				"tp2": {
					{
						targetGroups: []config.TargetGroup{},
						interval:     200,
					},
				},
				"tp3": {
					{
						targetGroups: []config.TargetGroup{},
						interval:     100,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{},
				{},
				{},
			},
		},
		{
			title: "Single TP initials only",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							}},
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
			},
		},
		{
			title: "Multiple TPs initials only",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp1-initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "tp1-initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
					},
				},
				"tp2": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp2-initial1",
								Targets: []model.LabelSet{{"__instance__": "3"}},
							},
						},
						interval: 10,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				}, {
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
					{
						Source:  "tp2-initial1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
				},
			},
		},
		{
			title: "Single TP initials followed by empty updates",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
						interval: 0,
					},
					{
						targetGroups: []config.TargetGroup{},
						interval:     10,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
				{},
			},
		},
		{
			title: "Single TP initials and new groups",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
						interval: 0,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "update1",
								Targets: []model.LabelSet{{"__instance__": "3"}},
							},
							{
								Source:  "update2",
								Targets: []model.LabelSet{{"__instance__": "4"}},
							},
						},
						interval: 10,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
				{
					{
						Source:  "update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
				},
			},
		},
		{
			title: "Multiple TPs initials and new groups",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp1-initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "tp1-initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
						interval: 10,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp1-update1",
								Targets: []model.LabelSet{{"__instance__": "3"}},
							},
							{
								Source:  "tp1-update2",
								Targets: []model.LabelSet{{"__instance__": "4"}},
							},
						},
						interval: 500,
					},
				},
				"tp2": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp2-initial1",
								Targets: []model.LabelSet{{"__instance__": "5"}},
							},
							{
								Source:  "tp2-initial2",
								Targets: []model.LabelSet{{"__instance__": "6"}},
							},
						},
						interval: 100,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp2-update1",
								Targets: []model.LabelSet{{"__instance__": "7"}},
							},
							{
								Source:  "tp2-update2",
								Targets: []model.LabelSet{{"__instance__": "8"}},
							},
						},
						interval: 10,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
				{
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
					{
						Source:  "tp2-initial1",
						Targets: []model.LabelSet{{"__instance__": "5"}},
					},
					{
						Source:  "tp2-initial2",
						Targets: []model.LabelSet{{"__instance__": "6"}},
					},
				},
				{
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
					{
						Source:  "tp2-update1",
						Targets: []model.LabelSet{{"__instance__": "7"}},
					},
					{
						Source:  "tp2-update2",
						Targets: []model.LabelSet{{"__instance__": "8"}},
					},
				},
				{
					{
						Source:  "tp1-update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "tp1-update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
					{
						Source:  "tp2-update1",
						Targets: []model.LabelSet{{"__instance__": "7"}},
					},
					{
						Source:  "tp2-update2",
						Targets: []model.LabelSet{{"__instance__": "8"}},
					},
				},
			},
		},
		{
			title: "One tp initials arrive after other tp updates.",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp1-initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "tp1-initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
						interval: 10,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp1-update1",
								Targets: []model.LabelSet{{"__instance__": "3"}},
							},
							{
								Source:  "tp1-update2",
								Targets: []model.LabelSet{{"__instance__": "4"}},
							},
						},
						interval: 150,
					},
				},
				"tp2": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp2-initial1",
								Targets: []model.LabelSet{{"__instance__": "5"}},
							},
							{
								Source:  "tp2-initial2",
								Targets: []model.LabelSet{{"__instance__": "6"}},
							},
						},
						interval: 200,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "tp2-update1",
								Targets: []model.LabelSet{{"__instance__": "7"}},
							},
							{
								Source:  "tp2-update2",
								Targets: []model.LabelSet{{"__instance__": "8"}},
							},
						},
						interval: 100,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "tp1-initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "tp1-initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
				{
					{
						Source:  "tp1-update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "tp1-update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
				},
				{
					{
						Source:  "tp1-update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "tp1-update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
					{
						Source:  "tp2-initial1",
						Targets: []model.LabelSet{{"__instance__": "5"}},
					},
					{
						Source:  "tp2-initial2",
						Targets: []model.LabelSet{{"__instance__": "6"}},
					},
				},
				{
					{
						Source:  "tp1-update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "tp1-update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
					{
						Source:  "tp2-update1",
						Targets: []model.LabelSet{{"__instance__": "7"}},
					},
					{
						Source:  "tp2-update2",
						Targets: []model.LabelSet{{"__instance__": "8"}},
					},
				},
			},
		},

		{
			title: "Single TP Single provider empty update in between",
			updates: map[string][]update{
				"tp1": {
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "initial1",
								Targets: []model.LabelSet{{"__instance__": "1"}},
							},
							{
								Source:  "initial2",
								Targets: []model.LabelSet{{"__instance__": "2"}},
							},
						},
						interval: 30,
					},
					{
						targetGroups: []config.TargetGroup{},
						interval:     10,
					},
					{
						targetGroups: []config.TargetGroup{
							{
								Source:  "update1",
								Targets: []model.LabelSet{{"__instance__": "3"}},
							},
							{
								Source:  "update2",
								Targets: []model.LabelSet{{"__instance__": "4"}},
							},
						},
						interval: 300,
					},
				},
			},
			expectedTargets: [][]*config.TargetGroup{
				{
					{
						Source:  "initial1",
						Targets: []model.LabelSet{{"__instance__": "1"}},
					},
					{
						Source:  "initial2",
						Targets: []model.LabelSet{{"__instance__": "2"}},
					},
				},
				{},
				{
					{
						Source:  "update1",
						Targets: []model.LabelSet{{"__instance__": "3"}},
					},
					{
						Source:  "update2",
						Targets: []model.LabelSet{{"__instance__": "4"}},
					},
				},
			},
		},
	}

	for testIndex, testCase := range testCases {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		discoveryManager := NewManager(nil)
		go discoveryManager.Run(ctx)

		var totalUpdatesCount int
		for tpName, update := range testCase.updates {
			provider := newMockDiscoveryProvider(update)
			discoveryManager.startProvider(ctx, poolKey{set: strconv.Itoa(testIndex), provider: tpName}, provider)

			if len(update) > 0 {
				totalUpdatesCount = totalUpdatesCount + len(update)
			}
		}

	Loop:
		for x := 0; x < totalUpdatesCount; x++ {
			select {
			case <-time.After(10 * time.Second):
				t.Errorf("%v. %q: no update arrived within the timeout limit", x, testCase.title)
				break Loop
			case tsetMap := <-discoveryManager.SyncCh():
				for _, received := range tsetMap {
					if !reflect.DeepEqual(received, testCase.expectedTargets[x]) {
						var receivedFormated string
						for _, receivedTargets := range received {
							receivedFormated = receivedFormated + receivedTargets.Source + ":" + fmt.Sprint(receivedTargets.Targets)
						}
						var expectedFormated string
						for _, expectedTargets := range testCase.expectedTargets[x] {
							expectedFormated = expectedFormated + expectedTargets.Source + ":" + fmt.Sprint(expectedTargets.Targets)
						}

						t.Errorf("%v. %v: \ntargets mismatch \nreceived: %v \nexpected: %v",
							x, testCase.title,
							receivedFormated,
							expectedFormated)
					}
				}
			}
		}
	}
}

func TestTargetSetRecreatesTargetGroupsEveryRun(t *testing.T) {
	verifyPresence := func(tSets map[poolKey][]*config.TargetGroup, poolKey poolKey, label string, present bool) {
		if _, ok := tSets[poolKey]; !ok {
			t.Fatalf("'%s' should be present in Pool keys: %v", poolKey, tSets)
			return
		}

		match := false
		var mergedTargets string
		for _, targetGroup := range tSets[poolKey] {

			for _, l := range targetGroup.Targets {
				mergedTargets = mergedTargets + " " + l.String()
				if l.String() == label {
					match = true
				}
			}

		}
		if match != present {
			msg := ""
			if !present {
				msg = "not"
			}
			t.Fatalf("'%s' should %s be present in Targets labels: %v", label, msg, mergedTargets)
		}
	}

	cfg := &config.Config{}

	sOne := `
scrape_configs:
 - job_name: 'prometheus'
   static_configs:
   - targets: ["foo:9090"]
   - targets: ["bar:9090"]
`
	if err := yaml.Unmarshal([]byte(sOne), cfg); err != nil {
		t.Fatalf("Unable to load YAML config sOne: %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	discoveryManager := NewManager(nil)
	go discoveryManager.Run(ctx)

	discoveryManager.ApplyConfig(cfg)

	_ = <-discoveryManager.SyncCh()
	verifyPresence(discoveryManager.targets, poolKey{set: "prometheus", provider: "static/0"}, "{__address__=\"foo:9090\"}", true)
	verifyPresence(discoveryManager.targets, poolKey{set: "prometheus", provider: "static/0"}, "{__address__=\"bar:9090\"}", true)

	sTwo := `
scrape_configs:
 - job_name: 'prometheus'
   static_configs:
   - targets: ["foo:9090"]
`
	if err := yaml.Unmarshal([]byte(sTwo), cfg); err != nil {
		t.Fatalf("Unable to load YAML config sOne: %s", err)
	}
	discoveryManager.ApplyConfig(cfg)

	_ = <-discoveryManager.SyncCh()
	verifyPresence(discoveryManager.targets, poolKey{set: "prometheus", provider: "static/0"}, "{__address__=\"foo:9090\"}", true)
	verifyPresence(discoveryManager.targets, poolKey{set: "prometheus", provider: "static/0"}, "{__address__=\"bar:9090\"}", false)
}

type update struct {
	targetGroups []config.TargetGroup
	interval     time.Duration
}

type mockdiscoveryProvider struct {
	updates []update
	up      chan<- []*config.TargetGroup
}

func newMockDiscoveryProvider(updates []update) mockdiscoveryProvider {

	tp := mockdiscoveryProvider{
		updates: updates,
	}
	return tp
}

func (tp mockdiscoveryProvider) Run(ctx context.Context, up chan<- []*config.TargetGroup) {
	tp.up = up
	tp.sendUpdates()
}

func (tp mockdiscoveryProvider) sendUpdates() {
	for _, update := range tp.updates {

		time.Sleep(update.interval * time.Millisecond)

		tgs := make([]*config.TargetGroup, len(update.targetGroups))
		for i := range update.targetGroups {
			tgs[i] = &update.targetGroups[i]
		}
		tp.up <- tgs
	}
}
