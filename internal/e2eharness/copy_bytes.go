package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// RunCopyByteCases exercises the shared copy engine through file contents.
// mv uses two filesystems so the rename fast path cannot hide a copy failure.
func RunCopyByteCases(t *testing.T, utility, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := utility
	if runtime.GOOS == "darwin" {
		name = "g" + name
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, utility)
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU " + utility)
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU " + utility)
	}
	var alphabet []byte
	for b := 0; b < 256; b++ {
		alphabet = append(alphabet, byte(b))
	}
	type copyCase struct {
		name   string
		data   []byte
		sparse bool
	}
	cases := []copyCase{
		{name: "empty"},
		{name: "all bytes", data: alphabet},
		{name: "zero tail", data: append(append([]byte{}, alphabet...), make([]byte, 262145)...)},
		{name: "zero middle", data: append(append(append([]byte{}, alphabet...), make([]byte, 262144)...), alphabet...)},
		{name: "all zero", data: make([]byte, 262145)},
	}
	for _, tc := range append([]copyCase{}, cases[2:]...) {
		tc.name = "sparse " + tc.name
		tc.sparse = true
		cases = append(cases, tc)
	}
	for _, n := range []int{131071, 131072, 131073, 262145} {
		cases = append(cases, copyCase{name: fmt.Sprintf("boundary %d", n), data: bytes.Repeat(alphabet, (n+255)/256)[:n]})
	}
	modes := []string{""}
	if utility == "cp" {
		modes = []string{"never", "auto", "always"}
	}
	for _, tc := range cases {
		for _, mode := range modes {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				outDir := dir
				if utility == "mv" {
					outDir = copyByteOtherDevice(t, dir)
				}
				for _, impl := range []struct {
					name, bin string
					runner    []string
				}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
					src := filepath.Join(dir, impl.name+"-input")
					dst := filepath.Join(outDir, impl.name+"-output")
					if err := writeCopyByteSource(src, tc.data, tc.sparse); err != nil {
						t.Fatal(err)
					}
					args := []string{src, dst}
					if utility == "cp" {
						args = append([]string{"--reflink=never", "--sparse=" + mode}, args...)
					} else if utility == "install" {
						args = append([]string{"-m", "644"}, args...)
					}
					command := append(append([]string{}, impl.runner...), impl.bin)
					command = append(command, args...)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, command[0], command[1:]...)
					cmd.Env = append(os.Environ(), "LC_ALL=C")
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					stdout, err := cmd.Output()
					if err != nil || len(stdout) != 0 {
						t.Fatalf("%s: %v stdout=%q stderr=%s", impl.name, err, stdout, stderr.Bytes())
					}
					if impl.name == "fern" && census != nil {
						census(t, stderr.String())
					} else if strings.TrimSpace(stderr.String()) != "" {
						t.Fatalf("%s stderr: %s", impl.name, stderr.Bytes())
					}
					got, err := os.ReadFile(dst)
					if err != nil || !bytes.Equal(got, tc.data) {
						t.Fatalf("%s content mismatch: %v, got %d bytes, want %d", impl.name, err, len(got), len(tc.data))
					}
					if utility == "mv" {
						if _, err := os.Stat(src); !os.IsNotExist(err) {
							t.Fatalf("%s did not remove source: %v", impl.name, err)
						}
					}
				}
			})
		}
	}
}

func writeCopyByteSource(path string, data []byte, sparse bool) error {
	if !sparse {
		return os.WriteFile(path, data, 0o644)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(int64(len(data))); err != nil {
		return err
	}
	for start := 0; start < len(data); start += 4096 {
		block := data[start:min(start+4096, len(data))]
		if bytes.Count(block, []byte{0}) != len(block) {
			if _, err := f.WriteAt(block, int64(start)); err != nil {
				return err
			}
		}
	}
	return nil
}
