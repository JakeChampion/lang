package semir

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/interp"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/ssa"
)

func lowerCheckedARM64(t *testing.T, source string) *ARM64Program {
	t.Helper()
	prog, info := checkedProgram(t, source)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	// Lowering may not mutate the semantic program or make its verifier
	// depend on the target's ABI-split values and RC calls.
	if err := VerifyProgram(p); err != nil {
		t.Fatal(err)
	}
	return out
}

var armLowerCases = []struct{ name, source, want string }{
	{"array", `function pilot(): string { var items = ["alpha", "beta"]; return items[1]; }`, "beta\n"},
	{"nested-array", `function pilot(): string { var items = [["left"], ["right"]]; return items[1][0]; }`, "right\n"},
	{"tuple", `function pilot(): string { var pair = (["tuple"], 7i64); let (items, _) = pair; return items[0]; }`, "tuple\n"},
	{"append", `function pilot(): string { var items = ["first"]; var more = items.append("last"); return more[1]; }`, "last\n"},
	{"append-nested", `function pilot(): string { var items = [["first"]]; var more = items.append(["last"]); return more[1][0]; }`, "last\n"},
	{"forward-call", `function pilot(): string { return get(["called"]); } function get(items: string[]): string { return items[0]; }`, "called\n"},
	{"owned-call", `function pilot(): string { return get(["consumed"]); } function get(own items: string[]): string { return items[0]; }`, "consumed\n"},
	{"borrow-and-consume", `function pilot(): string { return get(["anchored"]); } function get(own items: string[]): string { return take(items[0], items); } function take(item: string, own items: string[]): string { return item; }`, "anchored\n"},
	{"early-return", `function pilot(): string { return choose(["early"], true); } function choose(own items: string[], yes: boolean): string { if (yes) { return items[0]; } return "other"; }`, "early\n"},
	{"other-return", `function pilot(): string { return choose(["early"], false); } function choose(own items: string[], yes: boolean): string { if (yes) { return items[0]; } return "other"; }`, "other\n"},
	{"embedded-zero", `function pilot(): string { var items = ["a\0b"]; return items[0]; }`, "a\x00b\n"},
	{"generated-name-shadow", `function pilot(): string { return __semir_helper_2(["safe"]); } function __semir_helper_2(items: string[]): string { return items[0]; }`, "safe\n"},
	{"recursive-borrowed-return", `function pilot(): string { return descend([["recursive"]], false)[0]; }
function descend(items: string[][], stop: boolean): string[] {
  if (stop) { return items[0]; }
  var returned = descend(items, true); var wrapped = [returned]; return wrapped[0];
}`, "recursive\n"},
	{"mutual-counted-return", `function pilot(): string { return first([["mutual"]], false)[0]; }
function first(own items: string[][], stop: boolean): string[] {
  if (stop) { return items[0]; } return second(items, true);
}
function second(own items: string[][], stop: boolean): string[] { return first(items, stop); }
`, "mutual\n"},
	{"own-container-self-element-append", `function pilot(): string { return grow([["self"]])[1][0]; }
function grow(own items: string[][]): string[][] { return items.append(items[0]); }
`, "self\n"},
}

func printHarness(out *ARM64Program) string {
	f := ssa.NewFunc("__semir_test_entry")
	out.Functions[f.Name] = f
	b := &armBuilder{f: f, b: f.NewBlock(), l: &armLowerer{out: out}}
	value := b.call(out.Symbols["pilot"], 64, true)
	b.call("print", 32, false, value)
	b.call("__fern_str_dec", 64, true, value)
	underflow := b.call("__fern_rc_underflow_count", 32, false)
	f.SetRet(b.b, underflow)
	return f.Name
}

func armExecutable(t *testing.T, out *ARM64Program, entry string, optimize bool) []byte {
	t.Helper()
	if optimize {
		for _, f := range out.Functions {
			ssa.Optimize(f)
			if err := ssa.Verify(f); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Existing codegen global instrumentation is guarded by the repository's
	// shared mutex. The new semantic pass itself does not consult ABI globals.
	ast.CodegenMu.Lock()
	before := ast.LeakCheckEnabled
	ast.LeakCheckEnabled = true
	asm, err := arm64ssa.EmitAsmModule(out.Functions, entry, arm64ssa.DefaultNumAlloc, nil)
	ast.LeakCheckEnabled = before
	ast.CodegenMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	text, data, err := nativearm64.AssembleProgramWX(asm, nativeelf.SegmentAddrsWXArm64)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return nativeelf.StaticExecutableDataWX(text, data)
}

func armLauncher(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return ""
	}
	for _, name := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if os.Getenv("FERN_REQUIRE_ARM64_SSA_DIFF") != "" {
		t.Fatal("required ARM64 runtime is unavailable")
	}
	t.Skip("ARM64 Linux runtime or qemu-aarch64 required")
	return ""
}

func runARM64Pilot(t *testing.T, binary []byte) (string, string, int) {
	t.Helper()
	launcher := armLauncher(t)
	path := filepath.Join(t.TempDir(), "pilot")
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	if launcher != "" {
		cmd = exec.CommandContext(ctx, launcher, path)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func requireBalancedCensus(t *testing.T, stderr string) {
	t.Helper()
	parts := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(\d+)`).FindStringSubmatch(stderr)
	if len(parts) != 4 || parts[1] != parts[2] || parts[3] != "0" {
		t.Fatalf("unbalanced allocator census: %q", stderr)
	}
}

func TestARM64TypedLoweringAssembles(t *testing.T) {
	for _, tc := range slices.Concat(armLowerCases, sourceLoopCases, expressionFlowCases, matchFlowCases, tupleMatchCases, voidCallCases, effectFlowCases, cleanupActionCases, iterationCleanupCases, conditionalCleanupCases) {
		t.Run(tc.name, func(t *testing.T) {
			prog, _ := checkedProgram(t, tc.source)
			oracle := interp.New()
			for _, f := range prog.Funcs {
				oracle.Register(f)
			}
			value, err := oracle.CallByName("pilot", nil)
			if err != nil {
				t.Fatal(err)
			}
			str, ok := value.(interp.String)
			if !ok || string(str)+"\n" != tc.want {
				t.Fatalf("interpreter disagrees with expected bytes: %v", value)
			}
			out := lowerCheckedARM64(t, tc.source)
			if len(out.Positions) == 0 {
				t.Fatal("lost source positions")
			}
			armExecutable(t, out, printHarness(out), true)
		})
	}
}

func TestARM64TypedLoweringRuns(t *testing.T) {
	armLauncher(t)
	for _, tc := range slices.Concat(armLowerCases, sourceLoopCases, expressionFlowCases, matchFlowCases, tupleMatchCases, voidCallCases, effectFlowCases, cleanupActionCases, iterationCleanupCases, conditionalCleanupCases) {
		for _, optimize := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/raw", true: "/optimized"}[optimize], func(t *testing.T) {
				out := lowerCheckedARM64(t, tc.source)
				binary := armExecutable(t, out, printHarness(out), optimize)
				stdout, stderr, code := runARM64Pilot(t, binary)
				if code != 0 || stdout != tc.want {
					t.Fatalf("exit %d, stdout %q, stderr %q; want %q", code, stdout, stderr, tc.want)
				}
				requireBalancedCensus(t, stderr)
			})
		}
	}
}
