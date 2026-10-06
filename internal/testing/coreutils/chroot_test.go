package coreutils

import (
	"os"
	"path/filepath"
	"testing"
)

func init() {
	registerCorpus("chroot", chrootCases)
}

// chroot(1) moves the process root, optionally changes identity, then execs.
//
// PRIVILEGE DECIDES HOW FAR EVERY CASE GETS, and that is fine here rather
// than a problem, because the harness runs both sides on the same host: what
// a case compares is that Fern and GNU answer identically at whatever
// privilege the runner has. Which half runs, though, is worth knowing when
// reading a failure.
//
//   - Unprivileged (the GitHub runners, uid 1001): chroot(2) is EPERM and
//     nothing after it runs. The grammar, the usage errors and exit 125 are
//     what these cases gate.
//   - Root (scripts/devbox, the dev container): the chroot, the setgroups /
//     setgid / setuid and the exec all happen. `--groups=root,0 / id -G`
//     really changes the supplementary set of the child.
//
// One asymmetry is the KERNEL's and shows up in the errno for a NEWROOT that
// does not exist. Linux's sys_chroot resolves the path before checking
// CAP_SYS_CHROOT, so it answers ENOENT at any privilege; XNU's suser() check
// runs before namei and answers EPERM. Both sides see the same one, so the
// cases below still compare — this is a note for whoever reads the output,
// not a divergence.
//
// The quirks these cases exist for, all measured against GNU 9.4:
//
//   - --skip-chdir is refused unless NEWROOT RESOLVES to "/", so the test is
//     on canonicalize_file_name and not on the spelling: `/.` and `/tmp/..`
//     are permitted where `/tmp` is not.
//   - --userspec=user: means --userspec=user. Exactly ONE trailing colon
//     comes off, so `user::` still fails as a spec with an empty group.
//   - The --groups numeric grammar is xstrtoumax and NOT the userspec one:
//     "+0" is the number 0 (strtoumax eats the sign) where userspec treats
//     '+' as a marker, "-0" is refused outright rather than wrapping, and
//     the ceiling is MAXGID itself — 4294967295 parses and then fails at the
//     syscall, where 4294967296 is `invalid group`.
//   - A NUMERIC --groups token is looked up BY NAME first and takes that
//     group's gid, unless it is prefixed '+'. Leading whitespace is skipped,
//     trailing whitespace is a fault.
//   - --groups is strtok, so a run of commas is one separator and produces
//     no empty tokens: "," yields nothing and is `invalid group list ','`
//     while "" is accepted and changes nothing.
//   - A bad token reports per token and keeps going; a spec that yields no
//     tokens at all reports the whole list once.
//   - The userspec diagnostics carry NO spec — `invalid user`, not
//     `invalid user: 'x'` — because chroot prints gnulib's string alone
//     where chown appends the operand. Same for the '.' warning.
func chrootCases(t *testing.T) []invocation {
	return []invocation{
		// ---- usage faults ----
		{name: "no arguments"},
		{name: "unrecognized long option", args: []string{"--bogus"}},
		{name: "groups with no argument", args: []string{"--groups"}},
		{name: "userspec with no argument", args: []string{"--userspec"}},
		{name: "skip-chdir alone is still missing an operand", args: []string{"--skip-chdir"}},
		{name: "dashdash alone", args: []string{"--"}},
		{name: "empty newroot", args: []string{"", "true"}},
		{name: "short option does not exist", args: []string{"-g", "/", "true"}},

		// An ambiguous abbreviation, and one that is not. `--g` is unique to
		// --groups, `--s` to --skip-chdir; both are accepted.
		{name: "abbreviated groups", args: []string{"--grou=0", "/", "true"}},
		{name: "abbreviated groups to one letter", args: []string{"--g=0", "/", "true"}},
		{name: "abbreviated userspec", args: []string{"--user=root", "/", "true"}},
		{name: "abbreviated skip-chdir", args: []string{"--s", "/", "true"}},

		// ---- --skip-chdir's precondition ----
		{name: "skip-chdir with a non-root newroot", args: []string{"--skip-chdir", "/tmp", "true"}},
		{name: "skip-chdir with root", args: []string{"--skip-chdir", "/", "true"}},
		{name: "skip-chdir with a path that resolves to root", args: []string{"--skip-chdir", "/.", "true"}},
		{name: "skip-chdir with a dotdot path that resolves to root", args: []string{"--skip-chdir", "/tmp/..", "true"}},
		{name: "skip-chdir with a newroot that does not exist", args: []string{"--skip-chdir", "/no-such-root-chroot-probe", "true"}},

		// ---- the chroot itself ----
		{name: "root and a command", args: []string{"/", "true"}},
		{name: "newroot that does not exist", args: []string{"/no-such-root-chroot-probe", "true"}},
		{name: "newroot that is a regular file", args: []string{"/dev/null", "true"}},
		{name: "command not found inside the new root", args: []string{"/tmp", "true"}},
		{name: "command is an absolute path that does not exist", args: []string{"/", "/no-such-command-chroot-probe"}},
		// No command at all. SHELL is pointed at a program that exits
		// immediately rather than /bin/sh, which would be handed -i and wait
		// for input that never comes.
		{name: "no command runs the shell", args: []string{"/"}, env: []string{"SHELL=/bin/false"}},
		{name: "no command with no SHELL", args: []string{"/no-such-root-chroot-probe"}},

		// ---- --userspec ----
		{name: "userspec naming a user that does not exist", args: []string{"--userspec=no-such-user-probe", "/", "true"}},
		{name: "userspec with a trailing colon", args: []string{"--userspec=root:", "/", "true"}},
		{name: "userspec that is a bare colon", args: []string{"--userspec=:", "/", "true"}},

		// The SECOND pass is seeded by the first, so a half the spec does not
		// name keeps the value the OUTER root gave it. This is the only case
		// that can see it: every other one chroots to "/", where the two
		// passes read the same files and the seeding cannot show.
		//
		// The new root's passwd gives `daemon` the host's uid and a gid the
		// host does not, so `--userspec=daemon` must come out with the uid
		// from INSIDE and the gid from OUTSIDE. Reading both from inside —
		// which is what this corpus caught — prints the new root's gid.
		//
		// Unprivileged the chroot is EPERM for both sides and the case
		// compares that instead, which is the same bargain every other
		// chroot case makes.
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

		// ---- --groups ----
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

		// ---- the two together ----
		{name: "userspec and groups", args: []string{"--userspec=root:root", "--groups=root", "/", "id"}},
		{name: "userspec and empty groups", args: []string{"--userspec=root", "--groups=", "/", "id"}},
		{name: "groups without userspec", args: []string{"--groups=root", "/", "id", "-G"}},

		// ---- options after NEWROOT belong to the command ----
		{name: "option after newroot is the command's", args: []string{"/", "echo", "--help"}},
		{name: "dashdash before newroot", args: []string{"--", "/", "true"}},
		{name: "newroot that looks like an option after dashdash", args: []string{"--", "--groups", "true"}},
	}
}

func TestChroot(t *testing.T) {
	requireParity(t, "chroot", chrootCases(t))
}

// `--help` and `--version` carry our own text by design (docs/COREUTILS.md);
// everything else about them still matches, including that `--help` wins over
// a later usage error.
func TestChrootHelpVersion(t *testing.T) {
	requireHelp(t, "chroot", []string{"--help"}, 0)
	requireHelp(t, "chroot", []string{"--hel"}, 0)
	requireHelp(t, "chroot", []string{"--help", "x"}, 0)
	requireVersion(t, "chroot", []string{"--version"}, 0)
	requireVersion(t, "chroot", []string{"--vers"}, 0)
	requireVersion(t, "chroot", []string{"--version", "x"}, 0)
}

// seedMismatchedRoot builds a new root whose passwd disagrees with the host's
// about one user, which is the only way to observe how the two lookup passes
// combine. `daemon` is the user because every platform this corpus runs on
// ships it in the flat passwd file — Debian, Ubuntu and macOS all give it uid
// 1 — so the OUTER pass resolves it without the corpus having to create it.
//
// The gid inside is 99, which is not daemon's anywhere, so the two answers
// cannot coincide by accident.
//
// The program to exec is this tree's own `id`, because it is statically
// linked: a dynamically linked one would need its loader and libc inside the
// new root as well. Both sides exec the same bytes, so what the case compares
// is chroot's behaviour and not id's.
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
