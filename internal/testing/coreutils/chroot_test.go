package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("chroot", chrootCases)
}

func chrootCases(t *testing.T) []invocation {
	return []invocation{
		{name: "no arguments"},
		{name: "unrecognized long option", args: []string{"--bogus"}},
		{name: "groups with no argument", args: []string{"--groups"}},
		{name: "userspec with no argument", args: []string{"--userspec"}},
		{name: "skip-chdir alone is still missing an operand", args: []string{"--skip-chdir"}},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "empty newroot", args: []string{"", "true"}},
		{name: "short option does not exist", args: []string{"-g", "/", "true"}},

		{name: "abbreviated groups", args: []string{"--grou=0", "/", "true"}},
		{name: "abbreviated groups to one letter", args: []string{"--g=0", "/", "true"}},
		{name: "abbreviated userspec", args: []string{"--user=root", "/", "true"}},
		{name: "abbreviated skip-chdir", args: []string{"--s", "/", "true"}},

		{name: "skip-chdir with a non-root newroot", args: []string{"--skip-chdir", "/tmp", "true"}},
		{name: "skip-chdir with root", args: []string{"--skip-chdir", "/", "true"}},
		{name: "skip-chdir with a path that resolves to root", args: []string{"--skip-chdir", "/.", "true"}},
		{name: "skip-chdir with a dotdot path that resolves to root", args: []string{"--skip-chdir", "/tmp/..", "true"}},
		{name: "skip-chdir with a newroot that does not exist", args: []string{"--skip-chdir", "/no-such-root-chroot-probe", "true"}},

		{name: "root and a command", args: []string{"/", "true"}},
		{name: "newroot that does not exist", args: []string{"/no-such-root-chroot-probe", "true"}},
		{name: "newroot that is a regular file", args: []string{"/dev/null", "true"}},
		{name: "command not found inside the new root", args: []string{"/tmp", "true"}},
		{name: "command is an absolute path that does not exist", args: []string{"/", "/no-such-command-chroot-probe"}},
		{name: "no command runs the shell", args: []string{"/"}, env: []string{"SHELL=/bin/false"}},
		{name: "no command with no SHELL", args: []string{"/no-such-root-chroot-probe"}},

		{name: "userspec naming a user that does not exist", args: []string{"--userspec=no-such-user-probe", "/", "true"}},
		{name: "userspec with a trailing colon", args: []string{"--userspec=root:", "/", "true"}},
		{name: "userspec that is a bare colon", args: []string{"--userspec=:", "/", "true"}},

		{
			name:     "userspec keeps the outer gid for a half the spec does not name",
			args:     []string{"--userspec=daemon", "newroot", "/id", "-G"},
			seedTree: seedMismatchedRoot,
		},
		{name: "userspec that is two colons", args: []string{"--userspec=::", "/", "true"}},
		{name: "userspec that is empty", args: []string{"--userspec=", "/", "true"}},
		{name: "userspec by name and group", args: []string{"--userspec=root:root", "/", "id"}},
		{name: "userspec by number", args: []string{"--userspec=0:0", "/", "id"}},
		{name: "userspec user only", args: []string{"--userspec=root", "/", "id"}},
		{name: "userspec group only", args: []string{"--userspec=:root", "/", "id"}},
		{name: "userspec with a dot separator warns", args: []string{"--userspec=root.root", "/", "true"}},
		{name: "userspec dot separator with a bad group", args: []string{"--userspec=root.no-such-group-probe", "/", "true"}},
		{name: "userspec group that does not exist", args: []string{"--userspec=root:no-such-group-probe", "/", "true"}},

		{name: "groups empty", args: []string{"--groups=", "/", "id", "-G"}},
		{name: "groups is one comma", args: []string{"--groups=,", "/", "id", "-G"}},
		{name: "groups is several commas", args: []string{"--groups=,,,", "/", "id", "-G"}},
		{name: "groups with an empty token in the middle", args: []string{"--groups=0,,0", "/", "id", "-G"}},
		{name: "groups by name", args: []string{"--groups=root", "/", "id", "-G"}},
		{name: "groups by number", args: []string{"--groups=0", "/", "id", "-G"}},
		{name: "groups name and number", args: []string{"--groups=root,0", "/", "id", "-G"}},
		{name: "groups number forced with a plus", args: []string{"--groups=+0", "/", "id", "-G"}},
		{name: "groups negative zero", args: []string{"--groups=-0", "/", "id", "-G"}},
		{name: "groups negative one", args: []string{"--groups=-1", "/", "id", "-G"}},
		{name: "groups at the gid ceiling", args: []string{"--groups=4294967295", "/", "id", "-G"}},
		{name: "groups over the gid ceiling", args: []string{"--groups=4294967296", "/", "id", "-G"}},
		{name: "groups with leading space", args: []string{"--groups= 0", "/", "id", "-G"}},
		{name: "groups with trailing space", args: []string{"--groups=0 ", "/", "id", "-G"}},
		{name: "groups with a space after the plus", args: []string{"--groups=+ 0", "/", "id", "-G"}},
		{name: "groups with two pluses", args: []string{"--groups=++0", "/", "id", "-G"}},
		{name: "groups in hex", args: []string{"--groups=0x0", "/", "id", "-G"}},
		{name: "groups with a leading zero is decimal", args: []string{"--groups=07", "/", "id", "-G"}},
		{name: "groups naming nothing", args: []string{"--groups=no-such-group-probe", "/", "id", "-G"}},
		{name: "groups with a bad token before a good one", args: []string{"--groups=no-such-group-probe,root", "/", "id", "-G"}},
		{name: "groups with a good token before a bad one", args: []string{"--groups=root,no-such-group-probe", "/", "id", "-G"}},
		{name: "groups with two bad tokens", args: []string{"--groups=no-such-a-probe,no-such-b-probe", "/", "id", "-G"}},

		{name: "userspec and groups", args: []string{"--userspec=root:root", "--groups=root", "/", "id"}},
		{name: "userspec and empty groups", args: []string{"--userspec=root", "--groups=", "/", "id"}},
		{name: "groups without userspec", args: []string{"--groups=root", "/", "id", "-G"}},

		{name: "option after newroot is the command's", args: []string{"/", "echo", "--help"}},
		{name: "dashdash before newroot", args: []string{"--", "/", "true"}},
		{name: "newroot that looks like an option after dashdash", args: []string{"--", "--groups", "true"}},
	}
}

func TestChroot(t *testing.T) {
	requireParity(t, "chroot", chrootCases(t))
}

func TestChrootHelpVersion(t *testing.T) {
	requireHelp(t, "chroot", []string{"--help"}, 0)
	requireHelp(t, "chroot", []string{"--hel"}, 0)
	requireHelp(t, "chroot", []string{"--help", "x"}, 0)
	requireVersion(t, "chroot", []string{"--version"}, 0)
	requireVersion(t, "chroot", []string{"--vers"}, 0)
	requireVersion(t, "chroot", []string{"--version", "x"}, 0)
}

func seedMismatchedRoot(t *testing.T, dir string) {
	t.Helper()
	root := filepath.Join(dir, "newroot")
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatalf("new root: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, "etc", name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("passwd", "root:x:0:0:root:/:/nonexistent\ndaemon:x:1:99:daemon:/:/nonexistent\n")
	write("group", "root:x:0:\ninside:x:99:\n")

	probe, err := os.ReadFile(fernBin(t, "id"))
	if err != nil {
		t.Fatalf("read the id probe: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "id"), probe, 0o755); err != nil {
		t.Fatalf("install the id probe: %v", err)
	}
}
