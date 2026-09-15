package e2eselfhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This experiment is selected explicitly by the benchmark-only workflow.
func bufferProfileTarget(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Fatal("native Linux is required for timing and RSS measurements")
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x86-64-linux"
	case "arm64":
		return "arm64-linux"
	default:
		t.Fatal("unsupported native architecture")
		return ""
	}
}

func bufferProfileJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bufferProfileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func TestCIInstructionBufferBuild(t *testing.T) {
	root, bootstrap := os.Getenv("CI_BUFFER_BUILD_ROOT"), os.Getenv("CI_BUFFER_BOOTSTRAP")
	if root == "" {
		t.Skip("benchmark-only compiler fixture build")
	}
	if !filepath.IsAbs(root) || !filepath.IsAbs(bootstrap) {
		t.Fatal("build root and bootstrap must be absolute paths")
	}
	target := bufferProfileTarget(t)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sources")
	if err := os.CopyFS(dir, os.DirFS(writeSelfHostModloadProject(t))); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "asm_modload_run.fern")
	gen0 := filepath.Join(root, "gen0")
	if out, err := exec.Command(bootstrap, "-target", target, "-o", gen0, entry).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v\n%s", err, out)
	}
	units := emitAllWholeCompiler(t, nil, gen0, entry, dir, "gen1", target, pmEmitAllBatch, pmGoBuiltEmitMemoryMB)
	objects := unitObjPaths(t, dir, "gen1", units)
	args := append([]string{"-static", "-nostdlib", "-no-pie"}, objects...)
	args = append(args, "-o", filepath.Join(root, "gen1"))
	if out, err := exec.Command("gcc", args...).CombinedOutput(); err != nil {
		t.Fatalf("link gen1: %v\n%s", err, out)
	}
	drive := func(args ...string) (string, error) {
		out, err := exec.Command(gen0, append([]string{entry}, args...)...).Output()
		return string(out), err
	}
	jobs := planWholeCompilerUnits(t, drive, dir, "profile")
	var names []string
	for _, job := range jobs {
		names = append(names, "unit_"+pmUnitKey(job)+".s")
	}
	bufferProfileJSON(t, filepath.Join(root, "unit-names.json"), names)
	revision, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	bufferProfileJSON(t, filepath.Join(root, "build.json"), map[string]any{
		"revision": strings.TrimSpace(string(revision)), "target": target, "units": len(names),
		"bootstrap_sha256": bufferProfileHash(t, bootstrap), "gen0_sha256": bufferProfileHash(t, gen0),
		"gen1_sha256": bufferProfileHash(t, filepath.Join(root, "gen1")),
	})
}

func TestCIInstructionBufferMeasure(t *testing.T) {
	root := os.Getenv("CI_BUFFER_PROFILE_ROOT")
	if root == "" {
		t.Skip("benchmark-only native comparison")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("profile root must be absolute")
	}
	target := bufferProfileTarget(t)
	scale, generation := os.Getenv("CI_BUFFER_SCALE"), os.Getenv("CI_BUFFER_GENERATION")
	if generation != "gen0" && generation != "gen1" {
		t.Fatal("generation must be gen0 or gen1")
	}
	lo, hi := 0, 1
	if scale == "full" {
		lo, hi = 8, 16
	} else if scale != "pilot" {
		t.Fatal("scale must be pilot or full")
	}
	loadNames := func(label string) []string {
		b, err := os.ReadFile(filepath.Join(root, label, "unit-names.json"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		if err := json.Unmarshal(b, &names); err != nil || len(names) < hi {
			t.Fatalf("invalid unit manifest: %v", err)
		}
		return names
	}
	names := loadNames("baseline")
	results := filepath.Join(root, "results-"+scale+"-"+generation)
	if err := os.Mkdir(results, 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	emit := func(label, gen, source, output string, lo, hi int) map[string]any {
		if err := os.Mkdir(output, 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(root, label, gen),
			filepath.Join(root, source, "sources", "asm_modload_run.fern"), "-target", target,
			"-per-module-emit-all", "-assume-eligible", "-func-budget", "100",
			"-unit-range", strconv.Itoa(lo)+":"+strconv.Itoa(hi), "-out-dir", output)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		began := time.Now()
		_, err := cmd.Output()
		wall := time.Since(began).Seconds()
		row := map[string]any{"compiler": label, "generation": gen, "source": source,
			"unit_range": []int{lo, hi}, "wall_seconds": wall, "stderr": stderr.String(), "timed_out": ctx.Err() != nil}
		if cmd.ProcessState != nil {
			row["exit_code"] = cmd.ProcessState.ExitCode()
			row["cpu_seconds"] = (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()
			row["max_child_rss_bytes"] = cmd.ProcessState.SysUsage().(*syscall.Rusage).Maxrss * 1024
		}
		bufferProfileJSON(t, output+".json", row)
		if err != nil || ctx.Err() != nil {
			t.Fatalf("emit %s %s [%d:%d]: %v\n%s", label, gen, lo, hi, err, stderr.String())
		}
		return row
	}
	verify := func(output, source string, names []string) {
		entries, err := os.ReadDir(output)
		if err != nil || len(entries) != len(names) {
			t.Fatalf("unexpected emitted unit count in %s: %d, %v", output, len(entries), err)
		}
		hashes := make(map[string]string)
		for _, name := range names {
			expected := bufferProfileHash(t, filepath.Join(root, source, "sources", "gen1_out", name))
			actual := bufferProfileHash(t, filepath.Join(output, name))
			if actual != expected {
				t.Fatalf("assembly mismatch in %s/%s", output, name)
			}
			hashes[name] = actual
		}
		bufferProfileJSON(t, output+"-hashes.json", hashes)
	}
	var rows []map[string]any
	for index, label := range []string{"baseline", "candidate", "candidate", "baseline"} {
		output := filepath.Join(results, fmt.Sprintf("trial-%d-%s", index, label))
		row := emit(label, generation, "baseline", output, lo, hi)
		verify(output, "baseline", names[lo:hi])
		rows = append(rows, row)
		bufferProfileJSON(t, filepath.Join(results, "summary.json"), rows)
		t.Logf("%s: wall=%v cpu=%v peak_bytes=%v", label, row["wall_seconds"], row["cpu_seconds"], row["max_child_rss_bytes"])
	}
	// Also compare the self-built candidate's own compiler output with gen0.
	ownNames := loadNames("candidate")
	end := 1
	if scale == "full" {
		end = len(ownNames)
	}
	for begin := 0; begin < end; begin += 8 {
		limit := min(begin+8, end)
		output := filepath.Join(results, fmt.Sprintf("fixpoint-%d-%d", begin, limit))
		emit("candidate", "gen1", "candidate", output, begin, limit)
		verify(output, "candidate", ownNames[begin:limit])
	}
	if scale == "pilot" && time.Since(start) >= time.Minute {
		t.Fatal("pilot exceeded one minute")
	}
	bufferProfileJSON(t, filepath.Join(results, "verified.json"), map[string]any{
		"target": target, "cpus": runtime.NumCPU(), "scale": scale, "generation": generation,
		"trial_units": hi - lo, "fixpoint_units": end, "trials": len(rows), "complete": true,
	})
}
