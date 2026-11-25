package e2ecompiler

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/testing/corpus"
)

func TestSelfHostStdTestShardCoverage(t *testing.T) {
	for _, tls := range []bool{false, true} {
		all := selfHostStdTestCases(t, tls)
		t.Setenv("FERN_STDTEST_SHARD", "")
		if got := selfHostStdTestShard(t, all); !reflect.DeepEqual(got, all) {
			t.Fatal("the default selector changed the complete corpus")
		}
		for _, count := range []int{1, 4, 8} {
			seen := map[string]int{}
			for index := 0; index < count; index++ {
				t.Setenv("FERN_STDTEST_SHARD", fmt.Sprintf("%d/%d", index, count))
				for _, tc := range selfHostStdTestShard(t, all) {
					seen[tc.name]++
				}
			}
			for _, tc := range all {
				if seen[tc.name] != 1 {
					t.Errorf("TLS=%v count=%d: case %s occurs %d times", tls, count, tc.name, seen[tc.name])
				}
			}
		}
	}
}

// Each row of .github/stdtest-weights.txt names a case, so no row is dead.
// At four and at eight shards, the heaviest cases go to different shards,
// one for each shard. No shard holds more than its share of the total
// weight and the heaviest case.
func TestSelfHostStdTestShardWeights(t *testing.T) {
	weights, err := corpus.ReadWeights(langSrcAbs(t, ".github/stdtest-weights.txt"))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, tls := range []bool{false, true} {
		for _, tc := range selfHostStdTestCases(t, tls) {
			known[tc.name] = true
		}
	}
	for name := range weights {
		if !known[name] {
			t.Errorf(".github/stdtest-weights.txt weighs %q, which is not a std/test case", name)
		}
	}
	all := selfHostStdTestCases(t, false)
	weight := func(name string) int {
		if w, ok := weights[name]; ok {
			return w
		}
		return 1
	}
	total, heaviest := 0, 0
	byWeight := make([]string, 0, len(all))
	for _, tc := range all {
		total += weight(tc.name)
		heaviest = max(heaviest, weight(tc.name))
		byWeight = append(byWeight, tc.name)
	}
	sort.SliceStable(byWeight, func(a, b int) bool { return weight(byWeight[a]) > weight(byWeight[b]) })
	for _, count := range []int{4, 8} {
		owners := map[string]int{}
		for index := 0; index < count; index++ {
			t.Setenv("FERN_STDTEST_SHARD", fmt.Sprintf("%d/%d", index, count))
			load := 0
			for _, tc := range selfHostStdTestShard(t, all) {
				owners[tc.name] = index
				load += weight(tc.name)
			}
			if load > total/count+heaviest {
				t.Errorf("shard %d/%d holds %d s of %d s", index, count, load, total)
			}
		}
		seen := map[int]string{}
		for _, name := range byWeight[:count] {
			if other, ok := seen[owners[name]]; ok {
				t.Errorf("the heavy cases %s and %s share shard %d/%d", other, name, owners[name], count)
			}
			seen[owners[name]] = name
		}
	}
}
