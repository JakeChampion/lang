package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const readDirNameLengthsProg = `function main(): i32 {
    match (read_dir(args()[1])) {
        Ok(names) => {
            for n in names {
                print(n);
            }
        },
        Err(_) => { return 2; }
    }
    print("--");
    match (read_dir_ino(args()[1])) {
        Ok(es) => {
            for e in es {
                print(e.name);
            }
        },
        Err(_) => { return 3; }
    }
    match (remove_dir_all(args()[2])) {
        Ok(_) => { return 0; },
        Err(_) => { return 4; }
    }
}
`

func readDirLengthNames() []string {
	const pattern = "abc\x01xyz~"
	var names []string
	for n := 1; n <= 255; n++ {
		var b strings.Builder
		for b.Len() < n {
			b.WriteByte(pattern[b.Len()%len(pattern)])
		}
		names = append(names, b.String())
		if n >= 2 {
			names = append(names, b.String()[:n-2]+"é")
		}
	}
	return names
}

func TestSelfHostReadDirNameLengths(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "rd.fern")
	if err := os.WriteFile(src, []byte(readDirNameLengthsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	names := readDirLengthNames()
	want := append([]string{}, names...)
	sort.Strings(want)
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		listed := filepath.Join(dir, tg.target+"-listed")
		doomed := filepath.Join(dir, tg.target+"-doomed")
		nested := filepath.Join(doomed, names[len(names)-1])
		for _, d := range []string{listed, nested} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, n := range names {
			for _, d := range []string{listed, doomed, nested} {
				if n == names[len(names)-1] && d == doomed {
					continue
				}
				if err := os.WriteFile(filepath.Join(d, n), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		run := exec.Command(bin, listed, doomed)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin, listed, doomed)...)
		}
		out, _ := run.Output()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d", tg.target, got)
			continue
		}
		parts := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n--\n")
		if len(parts) != 2 {
			t.Errorf("%s: output is not two listings: %q", tg.target, out)
			continue
		}
		for i, reader := range []string{"read_dir", "read_dir_ino"} {
			got := strings.Split(parts[i], "\n")
			sort.Strings(got)
			if len(got) != len(want) {
				t.Errorf("%s: %s listed %d names, want the %d created", tg.target, reader, len(got), len(want))
				continue
			}
			for k := range got {
				if got[k] != want[k] {
					t.Errorf("%s: %s listed %q where %q was created", tg.target, reader, got[k], want[k])
					break
				}
			}
		}
		if _, err := os.Lstat(doomed); !os.IsNotExist(err) {
			t.Errorf("%s: remove_dir_all left %s: %v", tg.target, doomed, err)
		}
	}
}
