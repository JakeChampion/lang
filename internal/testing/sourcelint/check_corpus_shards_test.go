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
