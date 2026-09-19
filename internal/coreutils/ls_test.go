package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("ls", lsCases)
	registerCorpus("dir", dirCases)
	registerCorpus("vdir", vdirCases)
}

// lsTree builds the one tree every ls / dir / vdir case reads.
//
// ONE tree, not one per side, for the same reason stat's corpus has one:
// most of what a long listing prints is the machine's answer rather than
// the utility's — an inode number, a link count, an allocated block
// count, a timestamp — so two implementations can only be compared on
// those fields by pointing them at the same inode. Nothing here writes.
//
// The timestamps are PINNED, and the set is chosen for the six-month
// rule. That rule is a fixed 15778476 seconds rather than a calendar
// offset, so the fixtures sit either side of the 182.62-day mark with
// most of a day of slack each way — a suite that runs for an hour
// cannot walk one of them across the boundary. `future` is pinned ahead
// of now, where GNU has no grace at all and the year format starts at
// +1 second.
func lsTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	readPaths := []string{dir}
	j := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	write := func(path string, n int) {
		t.Helper()
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir := func(path string, mode os.FileMode) {
		t.Helper()
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		readPaths = append(readPaths, path)
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		readPaths = append(readPaths, path)
	}
	stamp := func(path string, sec int64) {
		t.Helper()
		tv := []syscall.Timespec{{Sec: sec, Nsec: 123456789}, {Sec: sec, Nsec: 123456789}}
		if err := syscall.UtimesNano(path, tv); err != nil {
			t.Fatal(err)
		}
	}

	// The eight S_IFMT kinds a mode column, an indicator and a colour
	// have to tell apart. A socket and a device node need privilege this
	// suite does not have, so they are absent: the two kinds the corpus
	// cannot make are covered by the /dev cases below, which read a tree
	// the machine already has.
	write(j("plain"), 3)
	write(j("empty"), 0)
	write(j("big"), 100000)
	mkdir(j("adir"), 0o755)
	mkdir(j("bdir"), 0o755)
	mkdir(j("bdir", "nested"), 0o755)
	write(j("bdir", "nested", "deep"), 1)
	write(j("adir", "inner"), 2)
	link("plain", j("link_ok"))
	link("nosuch", j("link_broken"))
	link("adir", j("link_dir"))
	seedFifo(t, j("afifo"))
	if err := os.Link(j("plain"), j("hardlink")); err != nil {
		t.Fatal(err)
	}

	// The permission and special bits the mode column and the colour
	// table switch on: setuid with and without the execute bit under it,
	// setgid the same, an executable, a sticky directory, an
	// other-writable one, and one that is both.
	write(j("exe"), 0)
	write(j("setuid"), 0)
	write(j("setgid"), 0)
	write(j("nox-setuid"), 0)
	if err := os.Chmod(j("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j("setuid"), os.ModeSetuid|0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j("setgid"), os.ModeSetgid|0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j("nox-setuid"), os.ModeSetuid|0o644); err != nil {
		t.Fatal(err)
	}
	mkdir(j("d-sticky"), os.ModeSticky|0o755)
	mkdir(j("d-otherw"), 0o777)
	mkdir(j("d-sticky-otherw"), os.ModeSticky|0o777)

	// Extensions, for `-X` and for the LS_COLORS suffix rules.
	write(j("a.c"), 1)
	write(j("b.h"), 1)
	write(j("c.txt"), 1)
	write(j("d.TXT"), 1)
	write(j("e.tar.gz"), 1)
	write(j("backup~"), 1)

	// Names the quoting styles and the `-q` substitution have to get
	// right: a space, an apostrophe, a double quote, a backslash, a
	// dollar, a tab, a newline, a control byte, and bytes that are not
	// ASCII. The last four are also what makes a column layout measure
	// the DISPLAYED width rather than the byte count.
	write(j("sp ace"), 1)
	write(j("quo'te"), 1)
	write(j("dq\"x"), 1)
	write(j("bs\\x"), 1)
	write(j("dollar$x"), 1)
	write(j("tab\tx"), 1)
	write(j("nl\nx"), 1)
	write(j("ctl\001x"), 1)
	write(j("utf8\303\251"), 1)

	// A dotfile, and one whose name sorts before the capitals, for `-a`
	// / `-A` and for the byte-wise sort having no dot rule.
	write(j(".dotfile"), 1)
	write(j(".hidden"), 1)
	mkdir(j(".dotdir"), 0o755)

	// Names whose byte order and whose filevercmp order differ, which is
	// the whole of what `-v` has to prove.
	for _, n := range []string{"Aa", "Bb", "aB", "a_b", "aa", "z1", "z9", "z10", "z100"} {
		write(j(n), 1)
	}

	// Sizes for `-S`, including a tie that the name has to break.
	write(j("s1"), 1)
	write(j("s5"), 5)
	write(j("s3a"), 3)
	write(j("s3b"), 3)

	// Times for `-t` and for the two long-format formats. `recent` and
	// `older` straddle the 182.62-day window; `epoch` and `y2020` are
	// far outside it; `future` is ahead of now, which takes the year
	// format with no grace.
	now := time.Now().Unix()
	write(j("t-recent"), 0)
	write(j("t-older"), 0)
	write(j("t-epoch"), 0)
	write(j("t-y2020"), 0)
	write(j("t-future"), 0)
	stamp(j("t-recent"), now-3600)
	stamp(j("t-older"), now-183*86400)
	stamp(j("t-epoch"), 0)
	stamp(j("t-y2020"), 1577934245)
	stamp(j("t-future"), now+86400*30)
	// atime, mtime and ctime all differ, so `-lu` / `-lc` / `-l` cannot
	// pass by printing one field three times.
	write(j("t-split"), 0)
	if err := os.Chtimes(j("t-split"), time.Unix(now-200*86400, 0), time.Unix(now-190*86400, 0)); err != nil {
		t.Fatal(err)
	}

	// An empty directory, and one holding a single entry, for the column
	// layout's degenerate cases and for the `total 0` line.
	mkdir(j("empty-dir"), 0o755)
	mkdir(j("one-dir"), 0o755)
	write(j("one-dir", "only"), 1)

	// A directory whose name needs quoting, for the header's own quoting
	// rules — where a COLON joins the escaped set and a space does not.
	mkdir(j("colon:dir"), 0o755)
	write(j("colon:dir", "in"), 1)
	mkdir(j("sp dir"), 0o755)
	write(j("sp dir", "in"), 1)

	// Recursive listings and readlink change access times even though no case
	// writes. Pin directory and symlink atimes ahead of both mtime and ctime
	// so Linux relatime cannot change them between the compared processes.
	// GNU touch -h sets the link's own time, including dangling links. Keep
	// mtimes and the explicit timestamp edge-case files unchanged.
	args := append([]string{"-h", "-a", "-d", "@" + strconv.FormatInt(now+30*86400, 10), "--"}, readPaths...)
	argv := crossArgv(referenceBin(t, "touch"), args...)
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("pin listing fixture access times: %v\n%s", err, out)
	}

	return dir
}

// lsCases is ls(1)'s corpus.
//
// The utility is three programs at once — an option scan, a set of
// sorts, and five output formats — so the corpus is organised the same
// way: every option on its own first, then the combinations where one
// changes what another does, then the layout grid, then the errors.
//
// Two whole areas are deliberately reachable WITHOUT a terminal, and
// that is measured rather than assumed. COLUMNS is read even when
// standard output is a pipe, so `-C -w N` and `COLUMNS=N -C` produce
// byte-identical output and the column formats need no pty; and
// `--color=always` / `--hyperlink=always` force what `auto` would
// decline.
func lsCases(t *testing.T) []invocation {
	return listingCases(t, "ls")
}

func dirCases(t *testing.T) []invocation {
	return listingCases(t, "dir")
}

func vdirCases(t *testing.T) []invocation {
	return listingCases(t, "vdir")
}

// One corpus for the three programs. `dir` is `ls -C -b` and `vdir` is
// `ls -l -b`, and the two defaults are all that differs — so running
// the same argv through each is what proves that, and it multiplies the
// coverage of every case below by three for free.
func listingCases(t *testing.T, util string) []invocation {
	dir := lsTree(t)
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir})
	}
	env := func(name string, envs []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, env: envs})
	}

	// --- one option at a time ---------------------------------------------
	add("bare")
	for _, flag := range []string{
		"-a", "-A", "-b", "-B", "-c", "-C", "-d", "-D", "-F", "-g",
		"-G", "-h", "-H", "-i", "-k", "-l", "-L", "-m", "-n", "-N", "-o",
		"-p", "-q", "-Q", "-r", "-R", "-s", "-S", "-t", "-u", "-U", "-v",
		"-x", "-X", "-Z", "-1",
	} {
		add("flag"+flag, flag)
	}
	for _, long := range []string{
		"--all", "--escape", "--directory", "--dired", "--full-time",
		"--group-directories-first", "--human-readable", "--inode",
		"--kibibytes", "--numeric-uid-gid", "--no-group",
		"--hide-control-chars", "--reverse", "--size", "--almost-all",
		"--ignore-backups", "--classify", "--file-type", "--si",
		"--dereference-command-line",
		"--dereference-command-line-symlink-to-dir", "--dereference",
		"--literal", "--quote-name", "--recursive", "--show-control-chars",
		"--zero", "--context", "--author",
	} {
		add("long"+long, long)
	}

	// --- the long format's columns -----------------------------------------
	// Each option that adds or drops one, and each that changes which way
	// a column pads: the owner and group names pad LEFT and `-n`'s
	// numeric ids pad RIGHT, which is the opposite.
	add("long-plain", "-l")
	add("long-all", "-la")
	add("long-numeric", "-ln")
	add("long-author", "-l", "--author")
	add("long-context", "-lZ")
	add("long-author-context", "-l", "--author", "-Z")
	add("long-no-owner", "-lg")
	add("long-no-group", "-lG")
	add("long-o", "-lo")
	add("long-inode", "-li")
	add("long-blocks", "-ls")
	add("long-inode-blocks", "-lis")
	add("long-everything", "-lisZ", "--author")
	add("long-one-file", "-l", "plain")
	add("long-dangling-link", "-l", "link_broken")
	add("long-dir-itself", "-ld", "adir")
	add("long-empty-dir", "-l", "empty-dir")

	// A device node and a socket, which the size column renders as
	// `major, minor` in two widths of its own. The corpus cannot create
	// either, so it reads /dev — whose entries both sides see the same
	// way, and where the majors reach three digits, so the device field
	// is wider than the file-size one and the two have to be reconciled.
	add("long-dev-null", "-l", "/dev/null")
	add("long-dev-pair", "-l", "/dev/null", "/dev/zero")
	add("long-dev-and-file", "-l", "/dev/null", "plain")
	add("long-dev-numeric", "-ln", "/dev/null")

	// --- the block and size units ------------------------------------------
	// `-k` moves the `total` and `-s` unit and NOT the size column, which
	// is why `ls -lk` still prints a file size in bytes.
	add("size-k", "-lk")
	add("size-k-blocks", "-sk")
	add("size-human", "-lh")
	add("size-human-blocks", "-lsh")
	add("size-si", "-l", "--si")
	add("size-block-1", "-l", "--block-size=1")
	add("size-block-512", "-l", "--block-size=512")
	add("size-block-1k", "-l", "--block-size=1K")
	add("size-block-suffix", "-l", "--block-size=K")
	add("size-block-kib", "-l", "--block-size=KiB")
	add("size-block-blocks", "-s", "--block-size=1")
	env("size-env-ls", []string{"LS_BLOCK_SIZE=K"}, "-s")
	env("size-env-block", []string{"BLOCK_SIZE=K"}, "-s")
	env("size-env-blocksize", []string{"BLOCKSIZE=K"}, "-s")
	env("size-env-unparsable", []string{"LS_BLOCK_SIZE=bogus"}, "-s")
	env("size-env-posix", []string{"POSIXLY_CORRECT=1"}, "-s")
	env("size-env-beaten-by-option", []string{"LS_BLOCK_SIZE=K"}, "-s", "-k")

	// --- sorting ------------------------------------------------------------
	// Every key, each with `-r`, because `-r` swaps the arguments of the
	// WHOLE comparison and so reverses the name tie-break too: `-Sr` over
	// two files of one size prints them backwards.
	for _, key := range []string{"-t", "-S", "-U", "-v", "-X"} {
		add("sort"+key, key)
		add("sort"+key+"-reverse", key, "-r")
		add("sort"+key+"-long", "-l", key)
	}
	add("sort-name-reverse", "-r")
	for _, word := range []string{"none", "time", "size", "extension", "version", "width"} {
		add("sort-word-"+word, "--sort="+word)
		add("sort-word-"+word+"-reverse", "--sort="+word, "-r")
	}
	add("sort-size-tie", "-S", "s1", "s3a", "s3b", "s5")
	add("sort-size-tie-reverse", "-Sr", "s1", "s3a", "s3b", "s5")
	add("sort-time-atime", "-tu")
	add("sort-time-ctime", "-tc")
	add("sort-time-word-atime", "-t", "--time=atime")
	add("sort-time-word-ctime", "-t", "--time=ctime")
	add("sort-dirs-first", "--group-directories-first")
	add("sort-dirs-first-reverse", "--group-directories-first", "-r")
	add("sort-dirs-first-long", "-l", "--group-directories-first")
	add("sort-dirs-first-time", "-t", "--group-directories-first")

	// --- which timestamp, and how it renders --------------------------------
	for _, word := range []string{"atime", "access", "use", "ctime", "status", "mtime", "modification"} {
		add("time-word-"+word, "-l", "--time="+word)
	}
	for _, style := range []string{
		"full-iso", "long-iso", "iso", "locale", "posix-full-iso",
		"posix-long-iso", "posix-iso", "posix-locale",
	} {
		add("time-style-"+style, "-l", "--time-style="+style)
	}
	add("time-style-format", "-l", "--time-style=+%Y-%m-%d")
	add("time-style-two-lines", "-l", "--time-style=+NONRECENT\n+RECENT")
	add("time-style-empty", "-l", "--time-style=+")
	add("time-full-time", "--full-time")
	add("time-full-time-beats-style", "--full-time", "--time-style=iso")
	add("time-style-beats-full-time", "--time-style=iso", "--full-time")
	env("time-style-env", []string{"TIME_STYLE=long-iso"}, "-l")
	env("time-style-env-beaten", []string{"TIME_STYLE=long-iso"}, "-l", "--time-style=iso")
	env("time-style-env-format", []string{"TIME_STYLE=+%Y"}, "-l")
	// The window is a fixed number of seconds and any future time takes
	// the year format, so these four files are the whole of the rule.
	add("time-recent-boundary", "-l", "t-recent", "t-older", "t-epoch", "t-future")
	add("time-recent-boundary-iso", "-l", "--time-style=iso", "t-recent", "t-older")
	add("time-split-mtime", "-l", "t-split")
	add("time-split-atime", "-lu", "t-split")
	add("time-split-ctime", "-lc", "t-split")

	// --- quoting -------------------------------------------------------------
	for _, style := range []string{
		"literal", "shell", "shell-always", "shell-escape",
		"shell-escape-always", "c", "c-maybe", "escape", "locale", "clocale",
	} {
		add("quote-style-"+style, "--quoting-style="+style)
		add("quote-style-"+style+"-long", "-l", "--quoting-style="+style)
	}
	env("quote-env", []string{"QUOTING_STYLE=shell-escape"})
	env("quote-env-bogus", []string{"QUOTING_STYLE=bogus"})
	env("quote-env-beaten", []string{"QUOTING_STYLE=c"}, "--quoting-style=literal")
	// `-q` substitutes AFTER quoting and only for the three styles that
	// can leave a non-printable byte behind, so `-q -b` is `-b` and
	// `-q --quoting-style=shell` leaves a control-byte name unquoted.
	add("quote-qmark", "-q")
	add("quote-qmark-escape", "-q", "-b")
	add("quote-qmark-c", "-q", "-Q")
	add("quote-qmark-shell", "-q", "--quoting-style=shell")
	add("quote-qmark-shell-always", "-q", "--quoting-style=shell-always")
	add("quote-qmark-shell-escape", "-q", "--quoting-style=shell-escape")
	// A hyperlinked name wearing outer quotes leaves them OUTSIDE the
	// link, but only under the padding regime: the quote is what the
	// bare names' leading space lines up with, so it belongs to the
	// column rather than to the link. Reachable down a pipe, which is
	// why these are here rather than among the terminal cases — the
	// aligned formats pad and the other two do not.
	add("link-quote-outside-C", "--hyperlink=always", "--quoting-style=shell-escape", "-C", "-w", "80")
	add("link-quote-outside-across", "--hyperlink=always", "--quoting-style=shell-escape", "-x", "-w", "80")
	add("link-quote-outside-long", "--hyperlink=always", "--quoting-style=shell-escape", "-l")
	add("link-quote-inside-commas", "--hyperlink=always", "--quoting-style=shell-escape", "-m", "-w", "80")
	add("link-quote-inside-one", "--hyperlink=always", "--quoting-style=shell-escape", "-1")
	add("link-quote-unlimited", "--hyperlink=always", "--quoting-style=shell-escape", "-C", "-w", "0")
	add("link-quote-coloured", "--hyperlink=always", "--quoting-style=shell-escape", "--color=always", "-C", "-w", "80")
	add("link-quote-dired", "--hyperlink=always", "--quoting-style=shell-escape", "-l", "--dired")
	add("link-quote-shell-always", "--hyperlink=always", "--quoting-style=shell-always", "-C", "-w", "80")
	add("quote-qmark-literal", "-q", "-N")
	add("quote-show-control", "--show-control-chars")
	add("quote-show-control-after-q", "-q", "--show-control-chars")
	add("quote-q-after-show-control", "--show-control-chars", "-q")
	add("quote-last-wins-b-then-N", "-b", "-N")
	add("quote-last-wins-N-then-b", "-N", "-b")
	// The header's set is a COLON, not a space.
	add("quote-header-colon", "-R", "colon:dir")
	add("quote-header-colon-escape", "-bR", "colon:dir")
	add("quote-header-space", "-bR", "sp dir")
	add("quote-header-styles", "-R", "--quoting-style=shell-always", "colon:dir")

	// --- the layouts ---------------------------------------------------------
	// A column layout is per-COLUMN widths with a floor of three, a count
	// that must be strictly under the width, and tab-aware padding — so
	// the grid sweeps the width across the interesting range at three tab
	// sizes, in both the down-then-across and across-then-down order.
	for _, w := range []string{"1", "2", "3", "5", "8", "13", "20", "26", "40", "60", "79", "80", "81", "200"} {
		add("cols-C-w"+w, "-C", "-w", w)
		add("cols-x-w"+w, "-x", "-w", w)
		add("cols-m-w"+w, "-m", "-w", w)
	}
	for _, ts := range []string{"0", "1", "3", "4", "8", "16"} {
		add("cols-tab"+ts, "-C", "-w", "60", "-T", ts)
		add("cols-tab"+ts+"-across", "-x", "-w", "60", "-T", ts)
		add("cols-tab"+ts+"-tabsize", "-C", "-w", "60", "--tabsize="+ts)
	}
	// A width of zero is unlimited and puts the whole listing on ONE
	// line, two spaces apart — the same writer `-m` uses with a comma.
	add("cols-unlimited-C", "-C", "-w", "0")
	add("cols-unlimited-x", "-x", "-w", "0")
	add("cols-unlimited-m", "-m", "-w", "0")
	add("cols-unlimited-one", "-1", "-w", "0")
	env("cols-env-columns", []string{"COLUMNS=40"}, "-C")
	env("cols-env-columns-across", []string{"COLUMNS=40"}, "-x")
	env("cols-env-columns-beaten", []string{"COLUMNS=40"}, "-C", "-w", "80")
	env("cols-env-columns-bogus", []string{"COLUMNS=abc"}, "-C")
	env("cols-env-columns-empty", []string{"COLUMNS="}, "-C")
	env("cols-env-columns-zero", []string{"COLUMNS=0"}, "-C")
	// The frills are part of the measured width.
	add("cols-with-inode", "-iC", "-w", "60")
	add("cols-with-blocks", "-sC", "-w", "60")
	add("cols-with-indicator", "-FC", "-w", "60")
	add("cols-with-all-frills", "-isFC", "-w", "60")
	add("cols-qmark-width", "-qC", "-w", "40")
	add("cols-escape-width", "-bC", "-w", "40")
	// A single entry, and none at all, in each layout.
	for _, f := range []string{"-C", "-x", "-m", "-1", "-l"} {
		add("cols-empty"+f, f, "empty-dir")
		add("cols-single"+f, f, "one-dir")
	}
	for _, word := range []string{"verbose", "long", "commas", "horizontal", "across", "vertical", "single-column"} {
		add("format-"+word, "--format="+word, "-w", "60")
	}
	// `-1` gives way to a long format that was typed and overrides one
	// that came from being `vdir`.
	add("format-1-then-l", "-1", "-l")
	add("format-l-then-1", "-l", "-1")
	add("format-C-then-l", "-C", "-l")
	add("format-l-then-C", "-l", "-C")
	add("format-m-then-x", "-m", "-x", "-w", "60")

	// --- --zero ---------------------------------------------------------------
	// It replaces the end of every ENTRY line and of the `total` line,
	// and nothing else: a directory header keeps its newline. And it
	// always asks for one per line, so a format option after it wins and
	// one before it does not.
	add("zero-plain", "--zero")
	add("zero-long", "--zero", "-l")
	add("zero-long-before", "-l", "--zero")
	add("zero-columns-after", "--zero", "-C", "-w", "60")
	add("zero-columns-before", "-C", "--zero", "-w", "60")
	add("zero-recursive", "--zero", "-R", "bdir")
	add("zero-multi-dir", "--zero", "adir", "bdir")
	add("zero-cancels-q", "-q", "--zero")
	add("zero-then-q", "--zero", "-q")

	// --- indicators -----------------------------------------------------------
	add("ind-classify", "-F")
	add("ind-classify-long", "-lF")
	add("ind-slash", "-p")
	add("ind-slash-long", "-lp")
	add("ind-file-type", "--file-type")
	add("ind-file-type-long", "-l", "--file-type")
	for _, word := range []string{"none", "slash", "file-type", "classify"} {
		add("ind-style-"+word, "--indicator-style="+word)
	}
	for _, when := range []string{"always", "yes", "force", "never", "no", "none", "auto", "tty", "if-tty"} {
		add("ind-classify-when-"+when, "--classify="+when)
	}
	// `-FC` is two options, not a WHEN of "C": only the long spelling
	// takes a value.
	add("ind-classify-short-takes-no-value", "-FC", "-w", "60")
	add("ind-last-wins", "-p", "-F")
	add("ind-style-beats-F", "-F", "--indicator-style=slash")

	// --- dereferencing --------------------------------------------------------
	add("deref-none", "-l", "link_ok", "link_dir", "link_broken")
	add("deref-all", "-lL", "link_ok", "link_dir", "link_broken")
	add("deref-all-short", "-L")
	add("deref-all-indicator", "-LF")
	add("deref-all-indicator-long", "-lLF")
	add("deref-command-line", "-lH", "link_dir", "link_ok")
	add("deref-command-line-dir", "-l", "--dereference-command-line-symlink-to-dir", "link_dir")
	add("deref-link-to-dir-listed", "link_dir")
	add("deref-link-to-dir-slash", "link_dir/")
	add("deref-link-itself", "-d", "link_dir")
	add("deref-link-itself-long", "-ld", "link_dir")

	// --- hiding ---------------------------------------------------------------
	add("hide-backups", "-B")
	add("hide-ignore", "-I", "a*")
	add("hide-ignore-twice", "-I", "a*", "-I", "b*")
	add("hide-ignore-bracket", "-I", "[ab]*")
	add("hide-ignore-question", "-I", "?dir")
	add("hide-ignore-escape", "-I", `a\.c`)
	add("hide-hide", "--hide=a*")
	add("hide-hide-cancelled-by-a", "-a", "--hide=a*")
	add("hide-hide-cancelled-by-A", "-A", "--hide=a*")
	add("hide-ignore-not-cancelled", "-a", "-I", "a*")
	add("hide-all-and-almost", "-a", "-A")
	add("hide-almost-and-all", "-A", "-a")

	// --- operands --------------------------------------------------------------
	add("operand-file", "plain")
	add("operand-two-files", "plain", "empty")
	add("operand-dir", "adir")
	add("operand-two-dirs", "adir", "bdir")
	add("operand-dirs-unsorted-typing", "bdir", "adir")
	add("operand-file-and-dir", "plain", "adir")
	add("operand-dir-and-file", "adir", "plain")
	add("operand-long-file-and-dir", "-l", "plain", "adir")
	add("operand-dot", ".")
	add("operand-dot-dot", "adir/..")
	add("operand-trailing-slash", "adir/")
	add("operand-double-slash", "adir//")
	add("operand-dash", "-")
	add("operand-dashdash", "--")
	add("operand-dashdash-then-flag", "--", "-l")
	add("operand-empty", "")
	add("operand-empty-long", "-l", "")
	add("operand-repeated", "plain", "plain")

	// --- recursion --------------------------------------------------------------
	add("recursive-bare", "-R")
	add("recursive-dir", "-R", "bdir")
	add("recursive-long", "-lR", "bdir")
	add("recursive-all", "-aR", "bdir")
	add("recursive-two", "-R", "adir", "bdir")
	add("recursive-columns", "-RC", "-w", "60")
	add("recursive-reverse", "-Rr", "bdir")
	add("recursive-indicator", "-RF", "bdir")

	// --- --dired -----------------------------------------------------------------
	// The offsets are byte positions into the whole of standard output,
	// so a case that changes anything before a name moves them — which is
	// what makes these worth having one apiece.
	add("dired-long", "-l", "--dired", "adir")
	add("dired-long-inode", "-li", "--dired", "adir")
	add("dired-recursive", "-lR", "--dired", "bdir")
	add("dired-link", "-l", "--dired", "link_ok")
	add("dired-escape", "-lb", "--dired", "sp dir")
	add("dired-no-long", "--dired", "adir")
	add("dired-columns", "--dired", "-C", "-w", "60")
	add("dired-empty", "-l", "--dired", "empty-dir")

	// --- colour --------------------------------------------------------------------
	// `auto` off a pipe is never, so the corpus forces `always`. The
	// built-in table is what LS_COLORS unset selects and is NOT what
	// `dircolors` writes — `mi`, `or`, `ca` and `mh` are unset here.
	for _, when := range []string{"always", "yes", "force", "never", "no", "none", "auto", "tty", "if-tty"} {
		env("color-when-"+when, []string{}, "--color="+when)
	}
	add("color-no-value", "--color")
	add("color-default-table", "--color=always")
	add("color-default-table-long", "--color=always", "-l")
	add("color-default-table-indicator", "--color=always", "-F")
	add("color-nothing-to-colour", "--color=always", "plain", "empty")
	for _, spec := range []string{
		"", "di=2", "di=", "fi=35", "no=4", "rs=7:di=2", "ec=END:di=2",
		"lc=<:rc=>:di=2", "ln=target:di=2", "or=42", "mi=41", "mh=33",
		"su=35:sg=36", "ex=32", "st=31:ow=32:tw=33",
		"*.txt=31", "*.TXT=31", "*.c=31:*.h=32", "*.gz=31", "*z=31:*.gz=32",
		"*.txt=31:*.txt=32", "di=2:di=3", "ca=30",
	} {
		env("color-spec-"+spec, []string{"LS_COLORS=" + spec}, "--color=always")
	}
	env("color-spec-escapes", []string{`LS_COLORS=di=\e[1m:ln=^[[2m`}, "--color=always")
	env("color-spec-octal", []string{`LS_COLORS=di=\061`}, "--color=always")
	env("color-spec-hex", []string{`LS_COLORS=di=\x31`}, "--color=always")
	env("color-spec-underscore", []string{`LS_COLORS=*sp\_ace=31`}, "--color=always")
	env("color-spec-unrecognized", []string{"LS_COLORS=zz=1:di=2"}, "--color=always")
	env("color-spec-no-equals", []string{"LS_COLORS=di"}, "--color=always")
	env("color-spec-leading-colon", []string{"LS_COLORS=:di=2"}, "--color=always")
	env("color-spec-trailing-colon", []string{"LS_COLORS=di=2:"}, "--color=always")
	env("color-spec-ignored-without-color", []string{"LS_COLORS=zz=1"})
	env("color-target-long", []string{"LS_COLORS=ln=36:di=2"}, "--color=always", "-l", "link_dir")
	env("color-target-missing", []string{"LS_COLORS=ln=36:mi=41"}, "--color=always", "-l", "link_broken")
	env("color-orphan", []string{"LS_COLORS=or=42"}, "--color=always", "-l", "link_broken")
	env("color-as-referent", []string{"LS_COLORS=ln=target:di=2"}, "--color=always", "-l", "link_dir", "link_broken")
	env("color-columns", []string{"LS_COLORS=di=2"}, "--color=always", "-C", "-w", "60")
	env("color-recursive", []string{"LS_COLORS=di=2"}, "--color=always", "-R", "bdir")
	env("color-after-f", []string{"LS_COLORS=di=2"}, "-f", "-t", "--color=always")
	env("color-f-unsorted", []string{"LS_COLORS=di=2"}, "-f", "--color=always")
	env("color-before-f", []string{"LS_COLORS=di=2"}, "--color=always", "-f", "-t")
	// 9.5 cut -f back to -a -U, so it no longer turns the long format,
	// -s or --hyperlink off along with the sorting.
	add("f-keeps-long", "-f", "-l")
	add("f-keeps-blocks", "-f", "-s")
	add("f-keeps-hyperlink", "-f", "--hyperlink=always")
	add("f-after-long", "-l", "-f")
	add("f-alone", "-f")

	// --- --hyperlink ------------------------------------------------------------------
	// The URI is the CANONICAL path with links resolved, percent-encoded
	// in lower-case hex — so a symbolic link points at its referent and a
	// broken one at the name it names.
	add("hyperlink-always", "--hyperlink=always")
	add("hyperlink-no-value", "--hyperlink")
	add("hyperlink-never", "--hyperlink=never")
	add("hyperlink-auto", "--hyperlink=auto")
	add("hyperlink-long", "--hyperlink=always", "-l")
	add("hyperlink-links", "--hyperlink=always", "-l", "link_ok", "link_dir", "link_broken")
	add("hyperlink-quoted", "--hyperlink=always", "sp ace", "dollar$x", "utf8\303\251")
	add("hyperlink-dir", "--hyperlink=always", "adir")

	// --- -f, which is three options at once ---------------------------------------------
	// `-f` is `-a` plus `-U`, so an unsorted listing carries `.` and `..`
	// wherever the directory itself holds them. That position is what
	// read_dir_all exists for (#9279); it is compared byte for byte here
	// like every other case, and one shared tree is what makes the
	// comparison meaningful — both sides read the same directory.
	add("f", "-f")
	add("f-one-per-line", "-f", "-1")
	add("f-long", "-f", "-l")
	add("f-unsorted-all", "-a", "-U")
	add("f-then-sort", "-f", "-t")
	add("f-then-almost-all", "-f", "-A")
	add("f-unsorted-almost-all", "-A", "-U")
	add("f-then-sort-size", "-f", "-S")
	// `.` and `..` reach the ignore checks like any other name, so -I can
	// take them out of an `-a` listing while -B cannot.
	add("all-ignore-every-dotfile", "-a", "-I", ".*")
	add("all-ignore-dot", "-a", "-I", ".")
	add("all-ignore-backups", "-a", "-B")

	// --- a real terminal on standard output -----------------------------------------------
	//
	// The half of the utility a pipe cannot reach. Four questions change
	// their answer: the DEFAULT format is columns rather than one per
	// line, the width comes from the terminal rather than from COLUMNS or
	// -w, the default QUOTING becomes shell-escape, and `auto` on the two
	// frills decides yes. The harness runs the terminal at 24x80, so the
	// width is the case's rather than a fallback's.
	tty := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, ttyOut: true})
	}
	ttyEnv := func(name string, envs []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, env: envs, ttyOut: true})
	}
	tty("tty-default")
	tty("tty-one-per-line", "-1")
	tty("tty-long", "-l")
	tty("tty-across", "-x")
	tty("tty-commas", "-m")
	tty("tty-width-beats-the-terminal", "-w", "40")
	ttyEnv("tty-columns-env-against-the-terminal", []string{"COLUMNS=40"})
	ttyEnv("tty-columns-env-zero", []string{"COLUMNS=0"})
	// A terminal that answers ENDS the width question, so this unparsable
	// COLUMNS is never looked at and nothing reaches stderr. The warning
	// the same value draws down a pipe is cols-env-columns-bogus above.
	ttyEnv("tty-columns-env-bogus", []string{"COLUMNS=abc"})
	ttyEnv("tty-columns-env-empty", []string{"COLUMNS="})
	tty("tty-color-auto", "--color=auto")
	tty("tty-color-never", "--color=never")
	tty("tty-hyperlink-auto", "--hyperlink=auto")
	tty("tty-quoting-default", "-a")
	tty("tty-quoting-literal", "-a", "--quoting-style=literal")
	tty("tty-quoting-long", "-la")
	tty("tty-quoting-escape-flag", "-ab")
	tty("tty-one-entry", "plain")
	tty("tty-empty-dir", "empty-dir")

	// --- errors ---------------------------------------------------------------------------
	add("err-missing", "nosuchfile")
	add("err-missing-long", "-l", "nosuchfile")
	add("err-missing-with-good", "nosuchfile", "plain")
	add("err-missing-with-dir", "nosuchfile", "adir")
	add("err-two-missing", "nosuchfile", "alsonot")
	add("err-missing-under-dir", "adir/nosuch")
	add("err-deref-dangling", "-lL", "link_broken")
	add("err-deref-dangling-under-dir", "-lL", ".")
	add("err-bad-option", "-z")
	add("err-bad-long-option", "--badopt")
	add("err-bad-long-option-with-value", "--badopt=1")
	add("err-ambiguous", "--=x")
	add("err-ambiguous-prefix", "--d")
	add("err-ambiguous-si", "--s")
	add("err-width-not-a-number", "-w", "abc")
	add("err-width-negative", "-w", "-1")
	add("err-width-empty", "--width=")
	add("err-tabsize-not-a-number", "-T", "abc")
	add("err-tabsize-empty", "--tabsize=")
	add("err-block-size-suffix", "--block-size=zz")
	add("err-block-size-huge", "--block-size=99999999999999999999999")
	add("err-block-size-empty", "--block-size=")
	add("err-width-requires-argument", "-w")
	add("err-ignore-requires-argument", "-I")
	add("err-hide-requires-argument", "--hide")
	add("err-sort-requires-argument", "--sort")
	add("err-sort-bogus", "--sort=bogus")
	add("sort-name", "--sort=name", "-U")
	add("sort-name-abbrev", "--sort=na", "-U")
	add("sort-n-is-ambiguous", "--sort=n")
	add("sort-name-beats-t", "-t", "--sort=name")
	add("err-sort-ambiguous", "--sort=t")
	add("err-format-bogus", "--format=bogus")
	add("err-format-ambiguous", "--format=ver")
	add("err-quoting-style-bogus", "--quoting-style=bogus")
	add("err-quoting-style-ambiguous", "--quoting-style=shell-e")
	add("err-indicator-style-bogus", "--indicator-style=bogus")
	add("err-time-bogus", "--time=bogus")
	add("err-time-ambiguous", "--time=c")
	add("err-color-bogus", "--color=bogus")
	add("err-color-ambiguous", "--color=a")
	add("err-hyperlink-bogus", "--hyperlink=bogus")
	add("err-classify-bogus", "--classify=bogus")
	add("err-time-style-bogus", "--time-style=bogus", "-l")
	add("err-time-style-three-lines", "--time-style=+a\n+b\n+c", "-l")
	add("err-dired-and-zero", "--dired", "--zero", "-l")
	add("err-zero-and-dired", "--zero", "--dired", "-l")
	add("err-no-option-after-dashdash", "--", "--all")

	// --- the write paths -----------------------------------------------------------------
	cases = append(cases,
		invocation{name: "write-error-full", args: []string{"-l"}, dir: dir, stdout: stdoutFull},
		invocation{name: "write-error-closed", args: []string{"-l"}, dir: dir, stdout: stdoutClosed},
		invocation{name: "write-error-full-plain", args: []string{}, dir: dir, stdout: stdoutFull},
		invocation{name: "write-error-closed-usage", args: []string{"-z"}, dir: dir, stdout: stdoutClosed},
		invocation{name: "write-error-full-columns", args: []string{"-C", "-w", "60"}, dir: dir, stdout: stdoutFull},
	)
	return cases
}

func TestLs(t *testing.T) {
	requireParity(t, "ls", lsCases(t))
}

func TestDir(t *testing.T) {
	requireParity(t, "dir", dirCases(t))
}

func TestVdir(t *testing.T) {
	requireParity(t, "vdir", vdirCases(t))
}

func TestLsHelp(t *testing.T) {
	requireHelp(t, "ls", []string{"--help"}, 0)
	requireHelp(t, "ls", []string{"--hel"}, 0)
	requireHelp(t, "ls", []string{"plain", "--help"}, 0)
	requireHelp(t, "dir", []string{"--help"}, 0)
	requireHelp(t, "vdir", []string{"--help"}, 0)
}

func TestLsVersion(t *testing.T) {
	requireVersion(t, "ls", []string{"--version"}, 0)
	requireVersion(t, "ls", []string{"--vers"}, 0)
	requireVersion(t, "ls", []string{"-l", "--version"}, 0)
	requireVersion(t, "dir", []string{"--version"}, 0)
	requireVersion(t, "vdir", []string{"--version"}, 0)
}
