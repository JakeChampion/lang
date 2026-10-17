package e2ecompiler

import (
	"debug/macho"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Cross-build every mode on Linux. The macOS lane additionally executes the
// timer/trampoline, checks named stacks, and asks Go's pprof to read the files.
func TestSelfHostDarwinProfile(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	var cli *selfHostCLI
	if native {
		dir := copySelfHostTree(t)
		cli = &selfHostCLI{
			bin:    buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern"),
			stdlib: e2eharness.SelfHostStdlibRoot(t),
		}
	} else {
		cli = buildSelfHostCLI(t)
	}
	for _, mode := range []string{"cpu", "alloc", "cpu-pprof", "alloc-pprof"} {
		t.Run(mode, func(t *testing.T) {
			source, want := cpuProfileHot, "-1611139248354046463\n"
			if strings.HasPrefix(mode, "alloc") {
				source, want = allocationProfileProgram, "523776\n"
			}
			dir := t.TempDir()
			const filename = "profile.pprof"
			bin := compileProfileWithEnv(t, cli, source, "arm64-darwin", mode, []string{"FERN_PROFILE_FILE=" + filename})
			image, err := macho.Open(bin)
			if err != nil {
				t.Fatal(err)
			}
			if image.Symtab == nil || len(image.Symtab.Syms) == 0 {
				t.Fatal("profile has no function symbols")
			}
			text, link := image.Segment("__TEXT"), image.Segment("__LINKEDIT")
			if text == nil || link == nil || link.Addr-text.Addr == link.Offset {
				t.Fatal("profile fixture must exercise the BSS gap in mapped __LINKEDIT")
			}
			image.Close()
			if !native {
				return
			}
			cmd := exec.Command(bin)
			cmd.Dir = dir
			stdout, stderr := runCPUProfile(t, cmd, 7)
			if stdout != want {
				t.Fatalf("stdout %q, want %q", stdout, want)
			}
			if strings.HasSuffix(mode, "pprof") {
				file := filepath.Join(dir, filename)
				function := "__fn_hot"
				if mode == "alloc-pprof" {
					function = "__fn_allocate"
					if top := pprofOutput(t, file, "-top", "-sample_index=alloc_objects"); !strings.Contains(top, "1024") {
						t.Fatalf("wrong allocation object count:\n%s", top)
					}
				}
				if top := pprofOutput(t, file, "-top"); !strings.Contains(top, function) {
					t.Fatalf("missing %s:\n%s", function, top)
				}
			} else if mode == "cpu" {
				checkCPUProfileHot(t, strings.ReplaceAll(stderr, "\n_main;", "\n_start;"))
			} else {
				lines := strings.Split(strings.TrimSpace(stderr), "\n")
				if len(lines) != 17 || lines[0] != "# fern allocation profile samples=16 dropped=0 interval=64 weight=rounded-bytes" {
					t.Fatalf("wrong allocation profile:\n%s", stderr)
				}
				for _, row := range lines[1:] {
					if !strings.HasPrefix(row, "_main;__fn_main;__fn_allocate;") || !strings.HasSuffix(row, " 2560") {
						t.Errorf("wrong allocation stack: %s", row)
					}
				}
			}
		})
	}
}
