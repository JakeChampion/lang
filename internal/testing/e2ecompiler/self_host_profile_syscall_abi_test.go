package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Raw syscall operands already name the target's kernel ABI. Darwin's
// assembly retargeting changes their number register and trap, not the value.
func TestSelfHostProfileSyscallABI(t *testing.T) {
	dir := copySelfHostTree(t)
	const driver = "import \"./rtprofile\";\nfunction main(): i32 {\n let av: string[] = args(); let mode: i32 = 1;\n if (av[2] == \"2\") { mode = 2; } else if (av[2] == \"3\") { mode = 3; } else if (av[2] == \"4\") { mode = 4; }\n print(rtprofile.source(av[1], mode)); return 0;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "profile_source.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	gcc, runner := x86_64Tooling(t)
	bin := buildSelfHostBin(t, gcc, dir, "profile_source.fern", "profile_source")
	call := regexp.MustCompile(`__syscall[0-6]\(\s*([0-9]+)`)
	for _, abi := range []struct {
		target                          string
		write, pid, action, timer, mask int
		allowed                         []int
	}{
		{"x86-64-linux", 1, 39, 13, 38, 14, []int{1, 13, 38, 231, 14, 257, 8, 9, 3, 11, 39}},
		{"arm64-linux", 64, 172, 134, 103, 135, []int{64, 134, 103, 94, 135, 56, 62, 222, 57, 215, 172}},
		{"arm64-darwin", 4, 20, 46, 83, 48, []int{4, 46, 83, 1, 48, 463, 199, 197, 6, 73, 20}},
	} {
		for mode := 1; mode <= 4; mode++ {
			t.Run(fmt.Sprintf("%s/%d", abi.target, mode), func(t *testing.T) {
				out, err := runX86_64Bin(runner, bin, abi.target, strconv.Itoa(mode)).Output()
				if err != nil {
					t.Fatal(err)
				}
				allowed, seen := map[int]bool{}, map[int]bool{}
				for _, number := range abi.allowed {
					allowed[number] = true
				}
				for _, match := range call.FindAllStringSubmatch(string(out), -1) {
					number, err := strconv.Atoi(match[1])
					if err != nil || !allowed[number] {
						t.Errorf("raw profiling syscall %s is outside %s's ABI", match[1], abi.target)
					}
					seen[number] = true
				}
				for _, number := range []int{abi.write, abi.pid, abi.action, abi.timer, abi.mask} {
					if !seen[number] {
						t.Errorf("missing profiling syscall %d for %s", number, abi.target)
					}
				}
				first := map[string]int{}
				for _, function := range strings.Split("\n"+string(out), "\nfunction ") {
					at := strings.Index(function, "(")
					match := call.FindStringSubmatch(function)
					if at >= 0 && match != nil {
						first[function[:at]], _ = strconv.Atoi(match[1])
					}
				}
				for name, want := range map[string]int{"__fern_profile_write_fd": abi.write, "__fern_profile_start": abi.pid} {
					if got, ok := first[name]; !ok || got != want {
						t.Errorf("%s first syscall = %d (present %v), want %d", name, got, ok, want)
					}
				}
			})
		}
	}
}
