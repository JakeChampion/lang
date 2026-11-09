package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type ownedLocalShadowCase struct {
	Name, Source, Want string
	Value              int
}

func ownedLocalShadowCases(t *testing.T) []ownedLocalShadowCase {
	t.Helper()
	data, err := os.ReadFile(langSrcAbs(t, "internal/check/checker/testdata/owned_local_shadow_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []ownedLocalShadowCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestSelfHostOwnedLocalShadowScopes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range ownedLocalShadowCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-android", "wasm32-wasi"} {
				cmd := exec.Command(cli.bin, "-stdlib", cli.stdlib, "-check", "-target", target, src)
				out, err := cmd.CombinedOutput()
				if tc.Want == "" {
					if err != nil {
						t.Fatalf("%s: %v\n%s", target, err, out)
					}
				} else if err == nil || !strings.Contains(string(out), "error["+tc.Want+"]") {
					t.Fatalf("%s: want %s, got %v\n%s", target, tc.Want, err, out)
				}
			}
		})
	}
}

func TestSelfHostOwnedLocalShadowRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range ownedLocalShadowCases(t) {
		if tc.Want != "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			source := tc.Source + "\nfunction main(): i32 { return work(State { n: 6, data: [1, 2, 3] }, true) - " + strconv.Itoa(tc.Value) + "; }\n"
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi", "arm64-android"} {
				output := filepath.Join(dir, target)
				flags := []string{"-stdlib", cli.stdlib, "-target", target, "-o", output}
				if target == "x86-64-linux" {
					flags = append(flags, "-sanitize")
				} else if target == "arm64-android" {
					flags = append(flags, "-shared", "-export", "main")
				}
				flags = append(flags, src)
				if out, err := exec.Command(cli.bin, flags...).CombinedOutput(); err != nil {
					t.Fatalf("%s compile: %v\n%s", target, err, out)
				}
				if target == "arm64-android" {
					continue
				}
				cmd := runX86_64Bin(cli.runner, output)
				if target == "wasm32-wasi" {
					cmd = exec.Command("wasmtime", "run", output)
				}
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s run: %v\n%s", target, err, out)
				}
				if target == "x86-64-linux" && !strings.Contains(string(out), "live_bytes=0") {
					t.Fatalf("missing clean sanitizer census: %s", out)
				}
			}
		})
	}
}

func TestSelfHostOwnedLocalShadowCheckerWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_local_shadow_checker.wasm", "wasm32-wasi")
	for _, tc := range ownedLocalShadowCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			cmd := exec.Command("wasmtime", "run", bin)
			cmd.Stdin = strings.NewReader(tc.Source)
			diags := driverDiags(runCheckerDriver(t, cmd, tc.Name))
			if tc.Want == "" && len(diags) == 0 {
				return
			}
			for _, d := range diags {
				if tc.Want != "" && d.code == tc.Want {
					return
				}
			}
			t.Fatalf("want %q, got %v", tc.Want, diags)
		})
	}
}
