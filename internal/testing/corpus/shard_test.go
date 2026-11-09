package corpus

import (
	"fmt"
	"reflect"
	"testing"
)

func TestShardCoversEveryFileOnce(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	files, err := Files(root, append(append([]string{}, Programs...), "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, 3, 4, 7} {
		seen := map[string]int{}
		for index := 0; index < count; index++ {
			part, err := Shard(files, fmt.Sprintf("%d/%d", index, count))
			if err != nil {
				t.Fatal(err)
			}
			if len(part) == 0 {
				t.Fatalf("empty shard %d/%d", index, count)
			}
			for _, file := range part {
				seen[file]++
			}
		}
		for _, file := range files {
			if seen[file] != 1 {
				t.Errorf("%d shards select %s %d times", count, file, seen[file])
			}
		}
	}
	for _, selector := range []string{"", "0/1"} {
		part, err := Shard(files, selector)
		if err != nil || !reflect.DeepEqual(part, files) {
			t.Errorf("selector %q changed the full corpus: %v", selector, err)
		}
	}
}

func TestShardRejectsInvalidSelectors(t *testing.T) {
	for _, selector := range []string{"0", "-1/4", "4/4", "0/0", "0/-1", "x/4", "0/x", "0/4/5"} {
		if _, err := Shard([]string{"a", "b"}, selector); err == nil {
			t.Errorf("invalid selector %q succeeded", selector)
		}
	}
}
