package coreutils

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRootRenameSafetyRejectsDifferentMounts(t *testing.T) {
	if err := rootRenameSafety(1, 1); err != nil {
		t.Fatalf("same-mount fixture refused: %v", err)
	}
	err := rootRenameSafety(1, 2)
	if err == nil || !strings.Contains(err.Error(), "TMPDIR") || !strings.Contains(err.Error(), "GNU's EXDEV") {
		t.Fatalf("different-mount fixture needs an actionable refusal, got %v", err)
	}
	for _, pair := range [][2]uint64{{0, 0}, {0, 1}, {1, 0}} {
		if err := rootRenameSafety(pair[0], pair[1]); err == nil {
			t.Fatalf("unknown mount identity admitted: %v", pair)
		}
	}
}

func linuxMountOf(t *testing.T, path string) uint64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s to verify the root-rename mount: %v", path, err)
	}
	defer f.Close()
	info, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", f.Fd()))
	if err != nil {
		t.Fatalf("read mount identity of %s before root rename: %v", path, err)
	}
	id, err := rootRenameMountID(string(info))
	if err != nil {
		t.Fatalf("verify mount identity of %s before root rename: %v", path, err)
	}
	return id
}

func rootRenameMountID(info string) (uint64, error) {
	for _, line := range strings.Split(info, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil && id != 0 {
				return id, nil
			}
			return 0, fmt.Errorf("invalid Linux mount identity %q", fields[1])
		}
	}
	return 0, fmt.Errorf("fdinfo has no Linux mount identity")
}

func TestRootRenameMountIDParsing(t *testing.T) {
	for _, info := range []string{"mnt_id:\t123\n", "pos:\t0\nflags:\t0100000\nmnt_id: 123\nino:\t42\n"} {
		if id, err := rootRenameMountID(info); err != nil || id != 123 {
			t.Fatalf("valid fdinfo rejected: id=%d err=%v", id, err)
		}
	}
	for _, info := range []string{"", "ino: 123\n", "mnt_id: 0\n", "mnt_id: -1\n", "mnt_id: x\n", "mnt_id: 18446744073709551616\n"} {
		if _, err := rootRenameMountID(info); err == nil {
			t.Fatalf("invalid mount identity admitted: %q", info)
		}
	}
}

func TestRootRenameLinuxMountIdentity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux fdinfo mount identity")
	}
	root := linuxMountOf(t, "/")
	if same := linuxMountOf(t, "/."); root != same {
		t.Fatalf("same root resolved to different mounts: %d != %d", root, same)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cwd, t.TempDir()} {
		mount := linuxMountOf(t, path)
		accepted := rootRenameSafety(root, mount) == nil
		if accepted != (root == mount) {
			t.Fatalf("kernel mount identities disagree with preflight for %s", path)
		}
		t.Logf("root device=%d mount=%d; fixture device=%d mount=%d; accepted=%t", deviceOf(t, "/"), root, deviceOf(t, path), mount, accepted)
	}
}

func TestMvRootOperandRequiresSafeDestination(t *testing.T) {
	seen := false
	for _, inv := range mvCases(t) {
		for _, arg := range inv.args {
			if strings.Trim(arg, "/") == "" && arg != "" && strings.Contains(arg, "//") {
				seen = true
				if !inv.rootRename || inv.seedTree == nil {
					t.Fatalf("root operand in %q lacks the pre-execution fixture guard", inv.name)
				}
			}
		}
	}
	if !seen {
		t.Fatal("missing root-operand regression case")
	}
}
