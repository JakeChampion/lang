package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var selfHostPassSwitches = []string{
	"FERN_SELFHOST_NO_REUSE=1",
	"FERN_SEM_REUSE=",
	"FERN_SEM_INLINE=",
	"FERN_NO_ARRAY_FUSION=1",
	"FERN_NO_SCALE_KERNEL=1",
	"FERN_NO_PRODUCT_KERNEL=1",
}

func selfHostPassConfig(seed uint64) []string {
	x := seed ^ 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	if x&1 == 0 {
		return nil
	}
	var env []string
	for i, sw := range selfHostPassSwitches {
		if x>>(i+1)&1 == 1 {
			env = append(env, sw)
		}
	}
	return env
}

func logPassConfig(t *testing.T, env []string) {
	t.Helper()
	if len(env) > 0 {
		t.Logf("pass configuration: %s", strings.Join(env, " "))
	}
}

func selfHostCompileCmd(fernBin string, env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(fernBin, args...)
	// The ambient environment follows SelfHostVerify, so a FERN_IR_VERIFY set
	// by whoever runs the leg wins.
	cmd.Env = append(append([]string{e2eharness.SelfHostVerify}, os.Environ()...), env...)
	return cmd
}

func TestSelfHostCompileCmdVerifiesTheIR(t *testing.T) {
	verify := func() string {
		for _, kv := range selfHostCompileCmd("fern", []string{"FERN_SEM_INLINE="}).Environ() {
			if v, ok := strings.CutPrefix(kv, "FERN_IR_VERIFY="); ok {
				return v
			}
		}
		return "unset"
	}
	t.Setenv("FERN_IR_VERIFY", "")
	os.Unsetenv("FERN_IR_VERIFY")
	if got := verify(); got != "quiet" {
		t.Errorf("a leg compile runs with FERN_IR_VERIFY=%s, want quiet", got)
	}
	t.Setenv("FERN_IR_VERIFY", "1")
	if got := verify(); got != "1" {
		t.Errorf("an ambient FERN_IR_VERIFY=1 became %s in a leg compile", got)
	}
}

func TestSelfHostPassConfigMix(t *testing.T) {
	const n = 512
	none, seen := 0, map[string]int{}
	for seed := uint64(0); seed < n; seed++ {
		env := selfHostPassConfig(seed)
		if len(env) == 0 {
			none++
		}
		for _, sw := range env {
			seen[sw]++
		}
	}
	if none < n/3 || none > 2*n/3 {
		t.Errorf("%d of %d seeds run the default configuration, want about half", none, n)
	}
	for _, sw := range selfHostPassSwitches {
		if seen[sw] < n/8 {
			t.Errorf("%s is set on %d of %d seeds, want about a quarter", sw, seen[sw], n)
		}
	}
}

func passEnv(passes func(uint64) []string, seed uint64) []string {
	if passes == nil {
		return nil
	}
	return passes(seed)
}

const selfHostBisectBudget = 16

var selfHostBisects atomic.Int64

func bisectSelfHostSeed(t *testing.T, fernBin, stdlibRoot, target, src string, flags, env []string, agrees func(env []string) bool) (report, pass string) {
	t.Helper()
	if selfHostBisects.Add(1) > selfHostBisectBudget {
		return fmt.Sprintf("decision bisect: skipped, %d seeds already bisected in this run", selfHostBisectBudget), "unbisected"
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	args := append(append([]string{"-target", target}, flags...), "-emit", "asm", "-o", filepath.Join(dir, "prog.s"), srcPath, stdlibRoot)
	out, hung, _ := compileSelfHostSeed(fernBin, append(append([]string{}, env...), "FERN_OPT_TRACE=1"), args...)
	if hung {
		return "decision bisect: the traced compile hung", "unbisected"
	}
	ds := e2eharness.ParseOptTrace(string(out))
	d, ok := e2eharness.BisectOptDecisions(ds, func(bound uint64) bool {
		return agrees(e2eharness.OptBisectEnv(env, bound))
	})
	if !ok {
		return fmt.Sprintf("decision bisect: the failure stays with all %d decisions declined, so no one pass decision causes it", len(ds)), "none"
	}
	return fmt.Sprintf("decision bisect: the failure follows `%s` (FERN_OPT_BISECT=%d agrees, FERN_OPT_BISECT=%d does not)", d, d.Hash+1, d.Hash), d.Pass
}

func seedRunAgrees(r *selfHostRun, gap string, want int, src string) bool {
	if r == nil || gap != "" {
		return false
	}
	bad := false
	checkSelfHostSeedExit(*r, want, src, func(string, ...any) { bad = true })
	return !bad && !strings.Contains(r.stderr, "fern-sanitizer:")
}
