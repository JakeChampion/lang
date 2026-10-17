package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func compilePprofProfile(t *testing.T, cli *selfHostCLI, src, target, mode, file string, flags ...string) string {
	t.Helper()
	return compileProfileWithEnv(t, cli, src, target, mode, []string{"FERN_PROFILE_FILE=" + file}, flags...)
}

func pprofOutput(t *testing.T, file string, args ...string) string {
	t.Helper()
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goTool, append(append([]string{"tool", "pprof"}, args...), file)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof: %v\n%.2000s", err, out)
	}
	return string(out)
}

func pprofSampleTotals(t *testing.T, file string) (int64, int64) {
	t.Helper()
	text := pprofOutput(t, file, "-raw")
	if !strings.Contains(text, "alloc_objects/count alloc_space/bytes") {
		t.Fatalf("missing allocation units: %.500s", text)
	}
	var objects, bytes int64
	for _, line := range strings.Split(text, "\n") {
		if line == "Locations" {
			break
		}
		left, _, ok := strings.Cut(line, ":")
		fields := strings.Fields(left)
		if !ok || len(fields) != 2 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		b, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		objects += n
		bytes += b
	}
	return objects, bytes
}

func TestSelfHostPprof(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var qemu string
			if target == "arm64-linux" {
				_, qemu = arm64Tooling(t)
				t.Setenv("QEMU_CPU", "max")
			}
			for _, mode := range []string{"cpu-pprof", "alloc-pprof"} {
				for _, harden := range []bool{false, true} {
					if harden && target != "arm64-linux" {
						continue
					}
					file := filepath.Join(t.TempDir(), "PROFILE_MODE_PROFILE_PATH_SYS_WRITE_\"東京\n.pprof")
					src, want := cpuProfileHot, "-1611139248354046463\n"
					if mode == "alloc-pprof" {
						src, want = allocationProfileProgram, "523776\n"
					}
					flags := []string{}
					if harden {
						flags = append(flags, "-harden")
					}
					bin := compilePprofProfile(t, cli, src, target, mode, file, flags...)
					cmd := runX86_64Bin(cli.runner, bin)
					if target == "arm64-linux" {
						cmd = runArm64Bin(qemu, bin)
					}
					stdout, stderr := runCPUProfile(t, cmd, 7)
					if stdout != want || stderr != "" {
						t.Fatalf("stdout %q stderr %q", stdout, stderr)
					}
					top := pprofOutput(t, file, "-top", "-cum")
					if mode == "alloc-pprof" {
						if !strings.Contains(top, "__fn_allocate") {
							t.Errorf("no allocating caller:\n%s", top)
						}
						objects, bytes := pprofSampleTotals(t, file)
						if objects != 1024 || bytes != 40960 {
							t.Errorf("objects=%d bytes=%d, want 1024/40960", objects, bytes)
						}
						if objTop := pprofOutput(t, file, "-top", "-sample_index=alloc_objects"); !strings.Contains(objTop, "1024") {
							t.Errorf("object view:\n%s", objTop)
						}
					} else {
						found := false
						for _, line := range strings.Split(top, "\n") {
							fields := strings.Fields(line)
							if len(fields) != 6 || fields[5] != "__fn_hot" {
								continue
							}
							flat, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64)
							if err != nil || flat < 75 {
								t.Errorf("hot function does not dominate: %s", line)
							}
							found = true
						}
						if !found {
							t.Errorf("no hot function:\n%s", top)
						}
					}
				}
			}
		})
	}
}

func TestSelfHostPprofExits(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		src, want string
		code      int
	}{
		{`function main(): i32 { print("normal"); eprint("ordinary diagnostic"); return 7; }`, "ordinary diagnostic\n", 7},
		{`function main(): i32 { exit(9); return 0; }`, "", 9},
		{`function main(): i32 { let xs: i32[] = [1, 2]; return xs[args().len()+4]; }`, "fern: array index out of range", 134},
	} {
		file := filepath.Join(t.TempDir(), "exit.pprof")
		if err := os.WriteFile(file, bytes.Repeat([]byte{'x'}, 100000), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := compilePprofProfile(t, cli, tc.src, "x86-64-linux", "cpu-pprof", file)
		_, stderr := runCPUProfile(t, runX86_64Bin(cli.runner, bin), tc.code)
		if !strings.HasPrefix(stderr, tc.want) {
			t.Errorf("stderr %q, want prefix %q", stderr, tc.want)
		}
		pprofOutput(t, file, "-raw")
		if info, err := os.Stat(file); err != nil || info.Size() >= 100000 {
			t.Errorf("profile did not replace the old file: %v %v", info, err)
		}
	}
	file := filepath.Join(t.TempDir(), "missing", "out.pprof")
	bin := compilePprofProfile(t, cli, `function main(): i32 { return 7; }`, "x86-64-linux", "alloc-pprof", file)
	_, stderr := runCPUProfile(t, runX86_64Bin(cli.runner, bin), 7)
	if !strings.Contains(stderr, "cannot create pprof output file") {
		t.Errorf("missing output failure: %q", stderr)
	}
	if runtime.GOOS == "linux" {
		bin := compilePprofProfile(t, cli, `function main(): i32 { return 7; }`, "x86-64-linux", "cpu-pprof", "/dev/full")
		_, stderr := runCPUProfile(t, runX86_64Bin(cli.runner, bin), 7)
		if stderr != "" {
			t.Errorf("output device did not open: %q", stderr)
		}
	}
}

func TestSelfHostPprofReservoirWeights(t *testing.T) {
	cli := buildSelfHostCLI(t)
	file := filepath.Join(t.TempDir(), "reservoir.pprof")
	src := strings.ReplaceAll(allocationProfileProgram, "1024", "530000")
	bin := compilePprofProfile(t, cli, src, "x86-64-linux", "alloc-pprof", file)
	runCPUProfile(t, runX86_64Bin(cli.runner, bin), 7)
	objects, bytes := pprofSampleTotals(t, file)
	if objects < 530000-64 || objects > 530000+64 {
		t.Errorf("scaled object estimate %d", objects)
	}
	if bytes < 530000*40-32768 || bytes > 530000*40+32768 {
		t.Errorf("scaled byte estimate %d", bytes)
	}
}
