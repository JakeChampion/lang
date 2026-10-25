package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestPrinterCorpusMatrixCoversEveryShard(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-units.yml"))
	if err != nil {
		t.Fatal(err)
	}
	gate := jobBlocks(string(data))["verify-heavy"]
	for _, required := range []string{"name: test-units-${{ matrix.host }}-heavy", "needs: test", "if: always()", "${{ needs.test.result }}", `test "$WORKER_RESULT" = success`} {
		if !strings.Contains(gate, required) {
			t.Errorf("aggregate unit check omits %q", required)
		}
	}
	// The matrix is host x group x shard, with the rest group's extra shards
	// excluded and the heavy denominator derived from the group. Every host
	// must schedule every heavy shard, and the denominator must be the shard
	// count.
	test := jobBlocks(string(data))["test"]
	list := func(key string) []string {
		m := regexp.MustCompile(`(?m)^\s*` + key + `:\s*\[([^\]]*)\]`).FindStringSubmatch(test)
		if m == nil {
			t.Fatalf("test-units.yml: the matrix has no `%s: [...]` dimension", key)
		}
		var out []string
		for _, v := range strings.Split(m[1], ",") {
			out = append(out, strings.TrimSpace(v))
		}
		return out
	}
	hosts, groups, shards := list("host"), list("group"), list("shard")
	if strings.Join(hosts, " ") != "x86_64 aarch64" || strings.Join(groups, " ") != "heavy rest" {
		t.Fatalf("test-units.yml: matrix hosts %v groups %v; want [x86_64 aarch64] and [heavy rest]", hosts, groups)
	}
	for i, sh := range shards {
		if sh != strconv.Itoa(i) {
			t.Fatalf("test-units.yml: shard list %v is not 0..n-1", shards)
		}
	}
	if strings.Contains(test, "group: heavy, shard") {
		t.Errorf("test-units.yml excludes a heavy shard; the printer corpus would run short")
	}
	denominator := regexp.MustCompile(`FERN_PRINTER_CORPUS_SHARD:\s*"\$\{\{ matrix\.shard \}\}/\$\{\{ matrix\.group == 'heavy' && (\d+) \|\| 1 \}\}"`).FindStringSubmatch(test)
	if denominator == nil {
		t.Fatalf("test-units.yml: FERN_PRINTER_CORPUS_SHARD is not derived from the group as `${{ matrix.shard }}/${{ matrix.group == 'heavy' && N || 1 }}`")
	}
	if n, _ := strconv.Atoi(denominator[1]); n != len(shards) {
		t.Errorf("test-units.yml: the heavy denominator is %d but the matrix schedules %d shards", n, len(shards))
	}
	for i := 1; i < len(shards); i++ {
		if !strings.Contains(test, "{ group: rest, shard: "+shards[i]+" }") {
			t.Errorf("test-units.yml: rest shard %s is not excluded, so the rest packages run %d times", shards[i], len(shards))
		}
	}
}
