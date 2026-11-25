package corpus

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestShardKeepsFilesWhenTheListChanges(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	files, err := Files(root, Programs)
	if err != nil {
		t.Fatal(err)
	}
	grown := append([]string{"examples/a_new_first_program.fern"}, files[:len(files)/2]...)
	grown = append(grown, "examples/a_new_middle_program.fern")
	grown = append(grown, files[len(files)/2+1:]...)
	for index := 0; index < 8; index++ {
		selector := fmt.Sprintf("%d/8", index)
		before, err := Shard(files, selector)
		if err != nil {
			t.Fatal(err)
		}
		after, err := Shard(grown, selector)
		if err != nil {
			t.Fatal(err)
		}
		owned := map[string]bool{}
		for _, file := range after {
			owned[file] = true
		}
		for _, file := range before {
			if file != files[len(files)/2] && !owned[file] {
				t.Errorf("shard %s lost %s when the list changed", selector, file)
			}
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

func weightedItems() ([]string, map[string]int) {
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h", "big1", "big2", "big3", "big4", "mid1", "mid2"}
	return items, map[string]int{"big1": 700, "big2": 480, "big3": 470, "big4": 460, "mid1": 100, "mid2": 90}
}

func weightedPartition(t *testing.T, items []string, weights map[string]int, count int) map[string]int {
	t.Helper()
	owner := map[string]int{}
	for index := 0; index < count; index++ {
		part, err := ShardByWeight(items, fmt.Sprintf("%d/%d", index, count), func(s string) string { return s }, weights)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range part {
			if previous, ok := owner[item]; ok {
				t.Fatalf("%d shards select %s in shards %d and %d", count, item, previous, index)
			}
			owner[item] = index
		}
	}
	for _, item := range items {
		if _, ok := owner[item]; !ok {
			t.Fatalf("%d shards do not select %s", count, item)
		}
	}
	return owner
}

func TestShardByWeightCoversEveryItemOnce(t *testing.T) {
	items, weights := weightedItems()
	for _, count := range []int{1, 2, 3, 4, 8, 20} {
		weightedPartition(t, items, weights, count)
	}
	for _, selector := range []string{"", "0/1"} {
		part, err := ShardByWeight(items, selector, func(s string) string { return s }, weights)
		if err != nil || !reflect.DeepEqual(part, items) {
			t.Errorf("selector %q changed the full list: %v", selector, err)
		}
	}
}

// The four heaviest items go to four different shards, and no shard holds
// much more weight than the heaviest item or an equal share of the total.
func TestShardByWeightSeparatesHeavyItems(t *testing.T) {
	items, weights := weightedItems()
	owner := weightedPartition(t, items, weights, 4)
	seen := map[int]string{}
	for _, big := range []string{"big1", "big2", "big3", "big4"} {
		if other, ok := seen[owner[big]]; ok {
			t.Errorf("%s and %s share shard %d", other, big, owner[big])
		}
		seen[owner[big]] = big
	}
	loads := make([]int, 4)
	for _, item := range items {
		w, ok := weights[item]
		if !ok {
			w = 1
		}
		loads[owner[item]] += w
	}
	for index, load := range loads {
		if load > 700+8 {
			t.Errorf("shard %d holds %d, more than the heaviest item and the light items", index, load)
		}
	}
}

// A light item that joins or leaves the list moves no heavier item.
func TestShardByWeightKeepsHeavyItemsWhenLightItemsChange(t *testing.T) {
	items, weights := weightedItems()
	before := weightedPartition(t, items, weights, 4)
	grown := append([]string{"a_new_light_item"}, items[1:]...)
	after := weightedPartition(t, grown, weights, 4)
	for item, w := range weights {
		if w > 1 && before[item] != after[item] {
			t.Errorf("%s moved from shard %d to %d when a light item changed", item, before[item], after[item])
		}
	}
}

func TestShardByWeightRejectsInvalidSelectors(t *testing.T) {
	for _, selector := range []string{"0", "-1/4", "4/4", "0/0", "x/4", "0/4/5"} {
		if _, err := ShardByWeight([]string{"a"}, selector, func(s string) string { return s }, nil); err == nil {
			t.Errorf("invalid selector %q succeeded", selector)
		}
	}
}

func TestReadWeights(t *testing.T) {
	dir := t.TempDir()
	write := func(text string) string {
		path := filepath.Join(dir, "weights.txt")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	got, err := ReadWeights(write("# a note\n\nbidi 701\n  opus_seek 482  \n"))
	if err != nil || !reflect.DeepEqual(got, map[string]int{"bidi": 701, "opus_seek": 482}) {
		t.Fatalf("ReadWeights = %v, %v", got, err)
	}
	for _, bad := range []string{"bidi\n", "bidi 7 8\n", "bidi x\n", "bidi 0\n", "bidi 3\nbidi 4\n"} {
		if _, err := ReadWeights(write(bad)); err == nil {
			t.Errorf("ReadWeights accepted %q", bad)
		}
	}
}
