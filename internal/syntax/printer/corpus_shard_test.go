package printer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

type corpusShard struct {
	index int
	count int
}

func parseCorpusShard(value string) (corpusShard, error) {
	if value == "" {
		return corpusShard{count: 1}, nil
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return corpusShard{}, fmt.Errorf("want index/count, got %q", value)
	}
	index, indexErr := strconv.Atoi(parts[0])
	count, countErr := strconv.Atoi(parts[1])
	if indexErr != nil || countErr != nil || count < 1 || index < 0 || index >= count {
		return corpusShard{}, fmt.Errorf("invalid corpus shard %q", value)
	}
	return corpusShard{index: index, count: count}, nil
}

func printerCorpusShard(t *testing.T) corpusShard {
	t.Helper()
	shard, err := parseCorpusShard(os.Getenv("FERN_PRINTER_CORPUS_SHARD"))
	if err != nil {
		t.Fatal(err)
	}
	return shard
}

func (s corpusShard) files(files []string) []string {
	if s.count == 1 {
		return files
	}
	var selected []string
	for i, file := range files {
		if i%s.count == s.index {
			selected = append(selected, file)
		}
	}
	return selected
}

func (s corpusShard) minimumChecked() int {
	minimum := 300 / s.count
	if 300%s.count != 0 {
		minimum++
	}
	return minimum
}

func TestCorpusShardAssignments(t *testing.T) {
	files := make([]string, 1003)
	for i := range files {
		files[i] = fmt.Sprintf("source-%04d.fern", i)
	}
	for _, count := range []int{1, 2, 3, 4, 7} {
		seen := map[string]bool{}
		minimum := 0
		for index := 0; index < count; index++ {
			shard := corpusShard{index: index, count: count}
			minimum += shard.minimumChecked()
			for _, file := range shard.files(files) {
				if seen[file] {
					t.Fatalf("%d shards duplicate %s", count, file)
				}
				seen[file] = true
			}
		}
		if len(seen) != len(files) || minimum < 300 {
			t.Fatalf("%d shards cover %d/%d files with a floor of %d", count, len(seen), len(files), minimum)
		}
	}
}

func TestCorpusShardConfiguration(t *testing.T) {
	for _, value := range []string{"", "0/1", "0/4", "3/4"} {
		if _, err := parseCorpusShard(value); err != nil {
			t.Errorf("valid %q: %v", value, err)
		}
	}
	for _, value := range []string{"0", "0/0", "-1/4", "4/4", "0/-1", "a/4", "0/b", "0/4/5"} {
		if _, err := parseCorpusShard(value); err == nil {
			t.Errorf("invalid %q was accepted", value)
		}
	}
}
