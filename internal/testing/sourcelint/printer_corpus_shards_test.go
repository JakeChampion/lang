package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestPrinterCorpusMatrixCoversEveryShard(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-units.yml"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`(?m)^\s*-\s*\{[^\n}]*host:\s*([A-Za-z0-9_]+)\s*,\s*group:\s*heavy\s*,\s*shard:\s*(\d+)\s*,\s*nshard:\s*(\d+)\s*\}`)
	seen := map[string]map[int]bool{}
	counts := map[string]int{}
	for _, match := range row.FindAllStringSubmatch(string(data), -1) {
		host := match[1]
		index, _ := strconv.Atoi(match[2])
		count, _ := strconv.Atoi(match[3])
		if count < 1 || index >= count {
			t.Fatalf("invalid %s shard %d/%d", host, index, count)
		}
		if seen[host] == nil {
			seen[host] = map[int]bool{}
			counts[host] = count
		}
		if count != counts[host] || seen[host][index] {
			t.Fatalf("inconsistent or duplicate %s shard %d/%d", host, index, count)
		}
		seen[host][index] = true
	}
	for _, host := range []string{"x86_64", "aarch64"} {
		if counts[host] < 1 || len(seen[host]) != counts[host] {
			t.Errorf("%s schedules %d of %d formatter corpus shards", host, len(seen[host]), counts[host])
		}
	}
}
