package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestUnitLaneCoversEveryNonSlowPackage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "unit-test-packages"))
	if err != nil {
		t.Fatalf("read unit-test-packages: %v", err)
	}
	src := string(b)
	re := regexp.MustCompile(`grep -vE '([^']+)'`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the exclusion alternation in scripts/unit-test-packages — if its shape changed, update this gate with it")
	}
	excluded := regexp.MustCompile(m[1])
	allowed := map[string]string{
		"e2e":         "test-e2e-{arm64,x86_64,wasm,other}",
		"e2ecompiler": "test-e2e-selfhost",
		"fernsmith":   "test-fernsmith",
		"coreutils":   "test-coreutils",
	}
	for pkg, lane := range allowed {
		if !excluded.MatchString("github.com/jakechampion/lang/internal/testing/" + pkg) {
			t.Errorf("internal/testing/%s is no longer excluded from the unit lane, but %s still runs it — it would now run twice", pkg, lane)
		}
	}
	for _, pkg := range []string{"e2eharness", "corpus", "sourcelint", "e2e/helper", "e2ecompiler/helper", "coreutils/helper"} {
		if excluded.MatchString("github.com/jakechampion/lang/internal/testing/" + pkg) {
			t.Errorf("the unit lane excludes %s without a recursive suite", pkg)
		}
	}
}

func TestSelfHostShardRoutesTestsToTheirPackage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the self-host shards run on Linux")
	}
	src := workflowSource(t, "test-e2e-selfhost.yml")
	start := strings.Index(src, "SEL_SH=()")
	end := strings.Index(src, "if [ ${#SEL_SH[@]} -eq 0 ]")
	if start < 0 || end <= start {
		t.Fatal("could not find the shard's package selection")
	}

	// A match at the start of a long list lets grep -q exit before printf
	// finishes. Under pipefail that used to route the test to the wrong binary.
	setup := `
RES_ALL=(TestFirst)
for ((i=0; i<20000; i++)); do RES_ALL+=("TestResidual$i"); done
MINE=(TestFirst TestCompiler TestResidual19999)
`
	report := `printf '%s\n' "${SEL_SH[*]}" "${SEL_RES[*]}"`
	cmd := exec.Command("bash", "-euo", "pipefail", "-c", setup+src[start:end]+report)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run the shard selection: %v\n%s", err, out)
	}
	if got, want := string(out), "TestCompiler\nTestFirst TestResidual19999\n"; got != want {
		t.Errorf("test lists by package = %q, want %q", got, want)
	}
}

func TestSelfHostShardsSelectEveryTest(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test-e2e-selfhost.yml: %v", err)
	}
	src := string(b)
	line := ""
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "mapfile -t SH_ALL") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("could not find the SH_ALL selection line in test-e2e-selfhost.yml")
	}
	if !strings.Contains(line, `-test.list '^Test'`) {
		t.Errorf("the e2ecompiler shards do not select every test:\n  %s\n"+
			"The whole package is self-host tests, so a narrower prefix drops the ones named outside it — "+
			"seven were running nowhere before `^Test` (#8471).", strings.TrimSpace(line))
	}
}
