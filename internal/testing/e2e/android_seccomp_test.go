package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The rows of a sandbox filter (asmcore.emit_seccomp_filter): four header
// rows, then one jeq row per syscall the image makes, then the kill that the
// list falls through to. The argument rules that follow the kill compare
// argument words, not syscall numbers.
var seccompRow = regexp.MustCompile(`(?m)^\s*\.long (\d+), (0x[0-9A-Fa-f]+|\d+)$`)

// filterSyscalls reads the syscall numbers from the `.Lseccomp_filter` rows of
// an image built with FERN_SANDBOX=1.
func filterSyscalls(asm string) ([]int, bool) {
	at := strings.Index(asm, ".Lseccomp_filter:")
	if at < 0 {
		return nil, false
	}
	var out []int
	for i, m := range seccompRow.FindAllStringSubmatch(asm[at:], -1) {
		if i < 4 {
			continue
		}
		code, _ := strconv.Atoi(m[1])
		if code&0xffff == 6 {
			break
		}
		if code&0xffff != 21 {
			continue
		}
		k, _ := strconv.ParseInt(m[2], 0, 64)
		out = append(out, int(k))
	}
	return out, true
}

func TestFilterSyscallsReadsTheJeqRows(t *testing.T) {
	// The rows after the kill are the argument rule of a socket call: a load
	// of the domain word and one jeq per admitted value, whose k is not a
	// syscall number.
	asm := ".Lseccomp_filter:\n    .long 32, 4\n    .long 65557, 0xC00000B7\n    .long 6, 0x80000000\n    .long 32, 0\n    .long 196629, 64\n    .long 131093, 93\n    .long 65557, 94\n    .long 6, 0x80000000\n    .long 32, 20\n    .long 131093, 0\n    .long 32, 16\n    .long 65557, 2\n    .long 6, 0x80000000\n    .long 6, 0x7FFF0000\n"
	got, ok := filterSyscalls(asm)
	if !ok || fmt.Sprint(got) != "[64 93 94]" {
		t.Fatalf("filterSyscalls = %v, %v; want [64 93 94]", got, ok)
	}
}

// Each syscall that an Android image of a fixture can make is one that
// Android's app seccomp filter permits; any other kills the app with SIGSYS.
// ANDROID_APP_SYSCALLS names the file that scripts/android-app-syscalls.py
// writes from AOSP; the Android lane makes it and runs this test.
func TestAndroidSeccompCorpus(t *testing.T) {
	path := os.Getenv("ANDROID_APP_SYSCALLS")
	if path == "" {
		t.Skip("set ANDROID_APP_SYSCALLS to the output of scripts/android-app-syscalls.py")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Tag      string           `json:"tag"`
		Syscalls map[string][]int `json:"syscalls"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	legs := []struct{ target, backend, abi string }{
		{"x86-64-android", "x86_64_android", "x86_64"},
		{"arm64-android", "arm64_android", "arm64"},
	}
	entries, err := os.ReadDir(conformanceCases)
	if err != nil {
		t.Fatal(err)
	}
	for _, leg := range legs {
		t.Run(leg.target, func(t *testing.T) {
			allowed := map[int]bool{}
			for _, n := range policy.Syscalls[leg.abi] {
				allowed[n] = true
			}
			if len(allowed) == 0 {
				t.Fatalf("%s has no %s syscalls", path, leg.abi)
			}
			// The first fixture that makes each syscall the filter refuses.
			refused := map[int]string{}
			checked := 0
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir, err := filepath.Abs(filepath.Join(conformanceCases, e.Name()))
				if err != nil {
					t.Fatal(err)
				}
				f := loadFixture(t, dir)
				if f.rejectionCase() || !f.backends[leg.backend] {
					continue
				}
				asmPath := filepath.Join(t.TempDir(), "prog.s")
				cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", leg.target, "-emit", "asm", "-o", asmPath, f.mainPath, e2eharness.SelfHostStdlibRoot(t))
				cmd.Env = e2eharness.ChildEnv("FERN_SANDBOX=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					// A number passed at run time has no allowlist; the
					// sandbox refuses the program, so it is not checked here.
					if strings.Contains(string(out), "passes the syscall number at run time") {
						continue
					}
					t.Fatalf("%s: build with FERN_SANDBOX=1: %v\n%s", e.Name(), err, out)
				}
				asm, err := os.ReadFile(asmPath)
				if err != nil {
					t.Fatal(err)
				}
				nums, ok := filterSyscalls(string(asm))
				if !ok {
					t.Fatalf("%s: the sandboxed image has no .Lseccomp_filter", e.Name())
				}
				checked++
				for _, n := range nums {
					if !allowed[n] {
						if _, seen := refused[n]; !seen {
							refused[n] = e.Name()
						}
					}
				}
			}
			if checked == 0 {
				t.Fatal("no fixture was checked")
			}
			var nums []int
			for n := range refused {
				nums = append(nums, n)
			}
			sort.Ints(nums)
			for _, n := range nums {
				t.Errorf("syscall %d is not in the Android app filter (%s); fixture %s makes it", n, policy.Tag, refused[n])
			}
			t.Logf("%d fixtures checked against the %s app filter of %s", checked, leg.abi, policy.Tag)
		})
	}
}
