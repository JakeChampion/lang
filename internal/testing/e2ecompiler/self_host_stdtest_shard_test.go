package e2ecompiler

import (
	"fmt"
	"reflect"
	"testing"
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
			owners := map[string]int{}
			for index := 0; index < count; index++ {
				t.Setenv("FERN_STDTEST_SHARD", fmt.Sprintf("%d/%d", index, count))
				for _, tc := range selfHostStdTestShard(t, all) {
					seen[tc.name]++
					owners[tc.name] = index
				}
			}
			for _, tc := range all {
				if seen[tc.name] != 1 {
					t.Errorf("TLS=%v count=%d: case %s occurs %d times", tls, count, tc.name, seen[tc.name])
				}
			}
			if !tls && count == 8 {
				large := map[int]string{}
				for _, name := range []string{"opus_multistream", "opus_file", "opus_seek", "opus_input"} {
					index, ok := owners[name]
					if !ok {
						t.Fatalf("missing full Opus case %s", name)
					}
					if previous, ok := large[index]; ok {
						t.Errorf("large cases %s and %s share shard %d", previous, name, index)
					}
					large[index] = name
				}
			}
		}
	}
}
