package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCheckerCorpusMatricesCoverEveryShard(t *testing.T) {
	readJobs := func(file string) map[string]string {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", file))
		if err != nil {
			t.Fatal(err)
		}
		return jobBlocks(string(data))
	}
	units := readJobs("test-units.yml")
	if !strings.Contains(units["test"], `FERN_CHECK_CORPUS_SHARD: "0/4"`) {
		t.Fatal("the regular unit jobs must run corpus shard 0/4")
	}
	for _, required := range []string{
		`contains(fromJSON(inputs.groups || '["heavy","rest"]'), 'rest')`,
		`inputs.packages == '' || inputs.packages == '[]' || contains(fromJSON(inputs.packages), './cmd/fern')`,
		`host: [x86_64, aarch64]`,
		`-run '^TestEveryExampleChecks$' ./cmd/fern`,
		`-timeout 25m`,
	} {
		if !strings.Contains(units["check-corpus"], required) {
			t.Errorf("unit corpus jobs omit %q", required)
		}
	}
	selfhost := readJobs("test-e2e-selfhost.yml")
	for _, tc := range []struct {
		name  string
		job   string
		first int
	}{
		{"unit corpus", units["check-corpus"], 1},
		{"stdlib checker", selfhost["checker-stdlib"], 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.job, `host: [x86_64, aarch64]`) {
				t.Fatal("corpus jobs must run on both native hosts")
			}
			denom := regexp.MustCompile(`FERN_CHECK_CORPUS_SHARD: "\$\{\{ matrix.shard \}\}/([0-9]+)"`).FindStringSubmatch(tc.job)
			matrix := regexp.MustCompile(`shard: \[([^\]]+)\]`).FindStringSubmatch(tc.job)
			if denom == nil || matrix == nil {
				t.Fatal("missing corpus denominator or shard matrix")
			}
			count, _ := strconv.Atoi(denom[1])
			values := strings.Split(matrix[1], ",")
			if count != 4 || len(values)+tc.first != count {
				t.Fatalf("matrix %v and first %d do not cover denominator %d", values, tc.first, count)
			}
			for i, value := range values {
				if strings.TrimSpace(value) != strconv.Itoa(i+tc.first) {
					t.Fatalf("matrix does not select each corpus shard once: %v", values)
				}
			}
			if strings.Contains(tc.job, "- shard:") || strings.Contains(tc.job, "- { shard:") {
				t.Fatal("corpus matrix excludes a shard")
			}
		})
	}
	if !strings.Contains(selfhost["checker-stdlib"], "-test.timeout 28m") {
		t.Fatal("stdlib checker timeout changed")
	}
}

func TestStdTestCorpusMatrixCoversEveryShard(t *testing.T) {
	job := jobBlocks(readWorkflow(t, mustRepoRoot(t), "test-e2e-selfhost.yml"))["selfhost-stdtest"]
	for _, required := range []string{
		`FERN_STDTEST_SHARD: "${{ matrix.shard }}/${{ matrix.count }}"`,
		`name: selfhost-stdtest-${{ matrix.leg }}-${{ matrix.shard }}`,
		`slot: stdtest-${{ matrix.leg }}-${{ matrix.shard }}`,
	} {
		if !strings.Contains(job, required) {
			t.Errorf("stdlib corpus jobs omit %q", required)
		}
	}
	expected := map[string]int{"x86-64": 8, "arm64": 8, "interp": 4, "x86-64-tls": 1, "arm64-tls": 1, "crypto-wasm": 1}
	rows := regexp.MustCompile(`\{ leg: ([a-z0-9-]+), shard: ([0-9]+), count: ([0-9]+) \}`).FindAllStringSubmatch(job, -1)
	seen := map[string]bool{}
	for _, row := range rows {
		count, _ := strconv.Atoi(row[3])
		index, _ := strconv.Atoi(row[2])
		want, ok := expected[row[1]]
		key := row[1] + "/" + row[2]
		if !ok || count != want || index >= count || seen[key] {
			t.Errorf("invalid or duplicate stdlib corpus row %s", row[0])
		}
		seen[key] = true
	}
	for leg, count := range expected {
		for index := 0; index < count; index++ {
			key := leg + "/" + strconv.Itoa(index)
			if !seen[key] {
				t.Errorf("missing stdlib corpus shard %s", key)
			}
		}
	}
	if strings.Count(job, "-test.timeout 33m") != 6 {
		t.Error("stdlib corpus timeouts changed")
	}
}
