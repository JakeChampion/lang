package coreutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	registerCorpus("chcon", chconCases)
}

func chconTree(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{"d/sub", "noperm/inner", "readable"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write f: %v", err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "sym")); err != nil {
		t.Fatalf("symlink sym: %v", err)
	}
	if err := os.Symlink("d", filepath.Join(dir, "symd")); err != nil {
		t.Fatalf("symlink symd: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangle")); err != nil {
		t.Fatalf("symlink dangle: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, "noperm"), 0o000); err != nil {
		t.Fatalf("chmod noperm: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "noperm"), 0o755) })
}

func chconCases(t *testing.T) []invocation {
	ctx := "unconfined_u:object_r:user_home_t:s0"
	cases := []invocation{
		{name: "no arguments", args: nil},
		{name: "a context and no file", args: []string{ctx}},
		{name: "a component option and no file", args: []string{"-u", "u"}},
		{name: "role and no file", args: []string{"-r", "r"}},
		{name: "type and no file", args: []string{"-t", "t"}},
		{name: "range and no file", args: []string{"-l", "l"}},
		{name: "reference and no file", args: []string{"--reference=f"}},
		{name: "an empty context and no file", args: []string{""}},
		{name: "the last operand is named after permutation", args: []string{"f", "--verbose"}},
		{name: "and after a long option", args: []string{"f", "--recursive"}},
		{name: "and after an abbreviated one", args: []string{"--p", "f"}},
		{name: "a dash is an operand", args: []string{"-"}},
		{name: "dashdash then one operand", args: []string{"--", ctx}},

		{name: "user wants a value", args: []string{"--user"}},
		{name: "role wants a value", args: []string{"--role"}},
		{name: "type wants a value", args: []string{"--type"}},
		{name: "range wants a value", args: []string{"--range"}},
		{name: "reference wants a value", args: []string{"--reference"}},
		{name: "short user wants a value", args: []string{"-u"}},
		{name: "short range wants a value", args: []string{"-l"}},

		{name: "invalid short option", args: []string{"-x"}},
		{name: "the dead -f of the switch", args: []string{"-f", ctx, "nosuchz"}},
		{name: "invalid option inside a cluster", args: []string{"-Rx", ctx, "nosuchz"}},
		{name: "unrecognized long option", args: []string{"--foo"}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar"}},
		{name: "empty long option", args: []string{"--="}},
		{name: "help takes no value", args: []string{"--help=x"}},
		{name: "version takes no value", args: []string{"--version=x"}},
		{name: "recursive takes no value", args: []string{"--recursive=x"}},
		{name: "verbose takes no value", args: []string{"--verbose=x"}},
		{name: "dereference takes no value", args: []string{"--dereference=x"}},
		{name: "preserve-root takes no value", args: []string{"--preserve-root=x"}},

		{name: "re is recursive or reference", args: []string{"--re=x", "f"}},
		{name: "r is four options", args: []string{"--r=x", "f"}},
		{name: "no is the two negatives", args: []string{"--no=x", "f"}},
		{name: "v is verbose or version", args: []string{"--v"}},
		{name: "a bare double dash prefix", args: []string{"--", "--r"}},
		{name: "rec is recursive alone", args: []string{"--rec", ctx, "nosuchz"}},
		{name: "de is dereference alone", args: []string{"--de", ctx, "nosuchz"}},
		{name: "p is preserve-root alone", args: []string{"--p", ctx, "nosuchz"}},

		{name: "recursive dereference needs H or L", args: []string{"-R", "--dereference", ctx, "f"}},
		{name: "P does not satisfy it", args: []string{"-R", "--dereference", "-P", ctx, "f"}},
		{name: "H does, so the walk starts", args: []string{"-R", "--dereference", "-H", ctx, "nosuchz"}},
		{name: "last traversal option wins", args: []string{"-R", "--dereference", "-H", "-P", ctx, "f"}},
		{name: "recursive h needs P", args: []string{"-R", "-h", "-L", ctx, "f"}},
		{name: "H also refuses h", args: []string{"-R", "-h", "-H", ctx, "f"}},
		{name: "P after L satisfies h", args: []string{"-R", "-h", "-L", "-P", ctx, "nosuchz"}},
		{name: "the whole cluster", args: []string{"-RHLP", ctx, "nosuchz"}},
		{name: "h and R clustered", args: []string{"-Rh", ctx, "nosuchz"}},
		{name: "the same the other way", args: []string{"-hR", ctx, "nosuchz"}},
		{name: "dereference then no-dereference", args: []string{"--dereference", "--no-dereference", ctx, "nosuchz"}},
		{name: "h outside a recursive walk", args: []string{"-h", ctx, "nosuchz"}},
		{name: "dereference outside one", args: []string{"--dereference", ctx, "nosuchz"}},
		{name: "L outside one", args: []string{"-L", ctx, "nosuchz"}},

		{name: "one missing operand", args: []string{ctx, "nosuchz"}},
		{name: "two missing operands", args: []string{ctx, "nosuchz", "nosuchz2"}},
		{name: "verbose says nothing for one", args: []string{"-v", ctx, "nosuchz"}},
		{name: "recursive over one", args: []string{"-R", ctx, "nosuchz"}},
		{name: "logical over one", args: []string{"-R", "-L", ctx, "nosuchz"}},
		{name: "comfollow over one", args: []string{"-R", "-H", ctx, "nosuchz"}},
		{name: "a missing component of a path", args: []string{ctx, "nosuchz/deeper"}},
		{name: "an empty operand", args: []string{ctx, ""}},
		{name: "an empty context", args: []string{"", "nosuchz"}},
		{name: "a context that is not valid UTF-8", args: []string{"\xff\xfe", "nosuchz"}},
		{name: "an operand that is not valid UTF-8", args: []string{ctx, "nosuch\xff\xfe"}},
		{name: "an operand with a newline", args: []string{ctx, "nosuch\nz"}},
		{name: "an operand with a quote", args: []string{ctx, "nosuch'z"}},
		{name: "an operand with a space", args: []string{ctx, "no such z"}},
		{name: "an operand after dashdash", args: []string{ctx, "--", "nosuchz"}},
		{name: "dashdash before everything", args: []string{"--", ctx, "nosuchz"}},
		{name: "an option-looking operand after dashdash", args: []string{"--", ctx, "-x"}},
		{name: "all four components then a missing file", args: []string{"-u", "u", "-r", "r", "-t", "t", "-l", "l", "nosuchz"}},
		{name: "a component option with an empty value", args: []string{"-u", "", ctx, "nosuchz"}},
		{name: "verbose twice", args: []string{"-vv", ctx, "nosuchz"}},
		{name: "preserve-root without recursive", args: []string{"--preserve-root", ctx, "nosuchz"}},
		{name: "no-preserve-root last", args: []string{"-R", "--preserve-root", "--no-preserve-root", ctx, "nosuchz"}},

		{name: "the root itself", args: []string{"-R", "--preserve-root", ctx, "/"}},
		{name: "two slashes are not the root", args: []string{"-R", "--preserve-root", ctx, "//"}},
		{name: "three slashes are", args: []string{"-R", "--preserve-root", ctx, "///"}},
		{name: "a dot under the root", args: []string{"-R", "--preserve-root", ctx, "/."}},
		{name: "a trailing slash", args: []string{"-R", "--preserve-root", ctx, "/./"}},
		{name: "climbing back to it", args: []string{"-R", "--preserve-root", ctx, "/usr/.."}},
		{name: "the failsafe is armed last", args: []string{"-R", "--no-preserve-root", "--preserve-root", ctx, "/"}},
	}

	if dirIsUnreadable(t) {
		for _, c := range []invocation{
			{name: "an unreadable directory", args: []string{"-R", ctx, "noperm"}},
			{name: "and verbosely", args: []string{"-R", "-v", ctx, "noperm"}},
			{name: "physically", args: []string{"-R", "-P", ctx, "noperm"}},
			{name: "logically", args: []string{"-R", "-L", ctx, "noperm"}},
			{name: "by a component option", args: []string{"-R", "-t", "t", "noperm"}},
			{name: "a missing operand beside it", args: []string{"-R", ctx, "noperm", "nosuchz"}},
		} {
			c.seedTree = chconTree
			cases = append(cases, c)
		}
	} else {
		t.Logf("running as a caller a mode-000 directory does not stop, so chcon's `cannot read directory` cases are not in this run")
	}

	if !hostHasSELinux() {
		for _, c := range []invocation{

			{name: "reference to a file", args: []string{"--reference=f", "f"}},
			{name: "reference that is missing", args: []string{"--reference=nosuchz", "f"}},
			{name: "reference through a link", args: []string{"--reference=sym", "f"}},
			{name: "reference beside a component", args: []string{"--reference=f", "-u", "u", "f"}},
			{name: "a user on an unlabeled file", args: []string{"-u", "u", "f"}},
			{name: "two components verbosely", args: []string{"-v", "-t", "t", "-r", "r", "f"}},
			{name: "a component through a link", args: []string{"-u", "u", "sym"}},
			{name: "a component on the link itself", args: []string{"-h", "-u", "u", "sym"}},
			{name: "a component on a dangling link", args: []string{"-h", "-u", "u", "dangle"}},
			{name: "a component down a tree", args: []string{"-R", "-u", "u", "d"}},
		} {
			c.seedTree = chconTree
			cases = append(cases, c)
		}
		for _, c := range []invocation{
			{name: "a context on a file", args: []string{ctx, "f"}},
			{name: "verbosely", args: []string{"-v", ctx, "f"}},
			{name: "on two files", args: []string{ctx, "f", "d"}},
			{name: "through a link", args: []string{ctx, "sym"}},
			{name: "on the link itself", args: []string{"-h", ctx, "sym"}},
			{name: "on a dangling link", args: []string{ctx, "dangle"}},
			{name: "down a tree", args: []string{"-R", "-v", ctx, "d"}},
			{name: "an empty context on a file", args: []string{"", "f"}},
			{name: "a file, then a missing one", args: []string{ctx, "f", "nosuchz"}},
		} {
			c.seedTree = chconTree
			c.context = true
			cases = append(cases, c)
		}
		if canLabel(t) {
			for _, c := range []invocation{
				{name: "a user on a labeled file", args: []string{"-u", "bob", "f"}},
				{name: "a range with colons", args: []string{"-v", "-l", "s0:c1,c2", "f"}},
				{name: "the type it already has", args: []string{"-t", "etc_t", "f"}},
				{name: "every component", args: []string{"-u", "u", "-r", "r", "-t", "t", "-l", "l", "f"}},
				{name: "a component through a link to it", args: []string{"-t", "t", "sym"}},
				{name: "reference to a labeled file", args: []string{"--reference=f", "d"}},
				{name: "reference beside a component, read", args: []string{"--reference=f", "-u", "u", "d"}},
			} {
				c.seedTree = chconLabeledTree
				c.context = true
				cases = append(cases, c)
			}
		} else {
			t.Logf("this caller may not write security.selinux, so chcon's labeled-file cases are not in this run")
		}
	}
	return cases
}

const chconLabel = "system_u:object_r:etc_t:s0"

func chconLabeledTree(t *testing.T, dir string) {
	t.Helper()
	chconTree(t, dir)
	if err := setEntryContext(filepath.Join(dir, "f"), chconLabel); err != nil {
		t.Fatalf("label f: %v", err)
	}
}

func canLabel(t *testing.T) bool {
	t.Helper()
	f := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	return setEntryContext(f, chconLabel) == nil
}

func hostHasSELinux() bool {
	b, err := os.ReadFile("/proc/self/mounts")
	return err == nil && strings.Contains(string(b), " selinuxfs ")
}

func dirIsUnreadable(t *testing.T) bool {
	t.Helper()
	d := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(d, 0o000); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	_, err := os.ReadDir(d)
	_ = os.Chmod(d, 0o755)
	return err != nil
}

func TestChcon(t *testing.T) {
	requireParity(t, "chcon", chconCases(t))
}

func TestChconHelp(t *testing.T) {
	requireHelp(t, "chcon", []string{"--help"}, 0)
	requireHelp(t, "chcon", []string{"--hel"}, 0)
	requireHelp(t, "chcon", []string{"-R", "--help"}, 0)
	requireHelp(t, "chcon", []string{"--help", "extra"}, 0)
	requireVersion(t, "chcon", []string{"--version"}, 0)
	requireVersion(t, "chcon", []string{"--vers"}, 0)
	requireVersion(t, "chcon", []string{"--version", "extra"}, 0)
}
