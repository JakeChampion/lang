package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("stat", statCases)
}

// statTree builds the one tree every case reads and hands back its path.
//
// ONE tree, not one per side, and that is what makes the corpus stable at
// all: almost everything stat prints is the machine's answer rather than
// the utility's — an inode number, a device id, an allocated block count,
// three timestamps — so the only way two implementations can be compared
// on those fields is to point them at the same inode. Nothing in the
// corpus writes, and stat(2) does not move an atime, so the record both
// sides read is the same record.
//
// What is NOT stable even then is the file SYSTEM's free counts. The two
// sides run seconds apart on a shared machine, and `stat -f -c '%f'` moved
// by tens of thousands of blocks between consecutive probe runs here. No
// case below ever renders one: the `-f` cases select the fields that do
// not move — the total block and inode counts, the block size, the name
// length — and `%a` / `%d` / `%f` appear only as the byte a directive
// under `H` hands back unanswered (`[%Ha]` is `?a`), where the count is
// not reached at all.
func statTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	j := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	write := func(path string, n int) {
		t.Helper()
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Go's FileMode does not spell the three special bits the way chmod(1)
	// does — 0o4000 in a FileMode is not ModeSetuid and is silently dropped
	// — so every mode here is written with the constants and the corpus
	// gets the file it asked for.
	mkdir := func(path string, mode os.FileMode) {
		t.Helper()
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	chmod := func(path string, mode os.FileMode) {
		t.Helper()
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}

	write(j("f"), 6)
	write(j("empty"), 0)
	write(j("big"), 5000)
	mkdir(j("d"), 0o755)
	mkdir(j("sub"), 0o755)
	write(j("sub", "inner"), 3)
	link("f", j("sl"))
	link("nowhere", j("dangle"))
	link("sub", j("dirlink"))
	if err := os.Link(j("f"), j("hard")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(j("fifo"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The permission alphabet %A and %a have to render: the three special
	// bits with and without the execute bit under them, an empty mode and
	// a full one.
	write(j("setuid"), 0)
	write(j("setgid"), 0)
	write(j("nox-setuid"), 0)
	write(j("nox-setgid"), 0)
	write(j("zeromode"), 0)
	write(j("allperm"), 0)
	write(j("setuid-setgid-sticky"), 0)
	mkdir(j("sticky"), os.ModeSticky|0o777)
	mkdir(j("sticky-nox"), os.ModeSticky|0o666)
	chmod(j("setuid"), os.ModeSetuid|0o755)
	chmod(j("setgid"), os.ModeSetgid|0o755)
	chmod(j("nox-setuid"), os.ModeSetuid|0o644)
	chmod(j("nox-setgid"), os.ModeSetgid|0o644)
	chmod(j("zeromode"), 0)
	chmod(j("allperm"), 0o777)
	chmod(j("setuid-setgid-sticky"), os.ModeSetuid|os.ModeSetgid|os.ModeSticky|0o707)

	// Pinned timestamps, so the cases that print a time exactly are
	// answering a date this corpus chose rather than whenever the suite
	// happened to run. The set is what the renderer has to get right: the
	// epoch itself, a whole second with no fraction, a fraction, and two
	// times BEFORE the epoch — one whole, one with a remainder, which is
	// where %.3Y has to carry the borrow (-1.5 seconds is -0.500, not
	// -1.500).
	stamp := func(path string, sec int64, nsec int64) {
		t.Helper()
		write(path, 0)
		tv := []syscall.Timespec{{Sec: sec, Nsec: nsec}, {Sec: sec, Nsec: nsec}}
		if err := syscall.UtimesNano(path, tv); err != nil {
			t.Fatal(err)
		}
	}
	stamp(j("t-epoch"), 0, 0)
	stamp(j("t-whole"), 981173106, 0)
	stamp(j("t-frac"), 981173106, 123456789)
	stamp(j("t-neg"), -315521755, 0)
	stamp(j("t-neg-frac"), -315521756, 500000000)
	stamp(j("t-leap"), 1709164800, 999999999)
	// atime and mtime differ from each other and from ctime, so %x / %y /
	// %z cannot pass by printing one field three times.
	if err := os.Chtimes(j("t-split"), time.Unix(1000000000, 111111111), time.Unix(2000000000, 222222222)); err != nil {
		write(j("t-split"), 0)
		if err := os.Chtimes(j("t-split"), time.Unix(1000000000, 111111111), time.Unix(2000000000, 222222222)); err != nil {
			t.Fatal(err)
		}
	}

	// Names a diagnostic and %N have to quote: a space, an apostrophe, a
	// double quote, a tab, a backslash, and bytes that are not UTF-8.
	write(j("sp ace"), 1)
	write(j("quo'te"), 1)
	write(j("dq\"x"), 1)
	write(j("tab\tx"), 1)
	write(j("back\\x"), 1)
	write(j("weird\xffname"), 1)
	link("sp ace", j("link to"))
	link("weird\xffname", j("weird\xfelink"))

	return dir
}

// statCases is stat(1)'s corpus.
//
// The utility is a format ENGINE with two alphabets, so the corpus is
// mostly formats: every directive of each mode, then the flag / width /
// precision grid over one directive of each of the three kinds (string,
// unsigned, signed-with-a-fraction), then the scanner's own edges — an
// unknown conversion, which prints `?` and swallows its byte, against an
// unknown conversion UNDER `H` or `L`, which prints `?` and gives the byte
// back, against `%%` wearing a modifier, which is fatal.
//
// The blocked directives (%w, %W, %C, and -f's %i, %S, %t, %T) are absent
// and so are the four built-in layouts that carry them — the default
// block, --terse, -f and -t -f. Those are refusals in this build and the
// header of coreutils/stat.fern says why; a case for one would be
// comparing our diagnostic against GNU's answer.
func statCases(t *testing.T) []invocation {
	dir := statTree(t)
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir})
	}
	env := func(name string, envs []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, env: envs})
	}

	// --- every file directive ---------------------------------------------
	// One case per directive rather than one case naming them all: a
	// failure then says which one, and a directive that crashes does not
	// take the rest of the alphabet with it.
	for _, spec := range []string{
		"%a", "%A", "%b", "%B", "%d", "%D", "%f", "%F", "%g", "%G", "%h",
		"%i", "%m", "%n", "%N", "%o", "%r", "%R", "%s", "%t", "%T", "%u",
		"%U", "%x", "%X", "%y", "%Y", "%z", "%Z", "%Hd", "%Ld", "%Hr", "%Lr",
	} {
		add("file-directive-"+spec, "-c", spec, "f")
	}
	add("file-all-directives", "-c", "%a %b %B %d %D %f %g %h %i %o %r %R %s %t %T %u", "f")
	add("file-all-strings", "-c", "%A|%F|%G|%U|%n|%N|%m", "f")

	// --- the record, per file kind ----------------------------------------
	for _, name := range []string{
		"f", "empty", "big", "d", "sub", "sl", "dangle", "dirlink", "hard",
		"fifo", "setuid", "setgid", "nox-setuid", "nox-setgid", "zeromode",
		"allperm", "sticky", "sticky-nox", "setuid-setgid-sticky",
	} {
		add("kind-"+name, "-c", "%A|%a|%#a|%f|%F|%h|%s", name)
	}
	add("kind-chardev", "-c", "%A|%F|%f|%r|%R|%t|%T|%Hr|%Lr|%Hd|%Ld", "/dev/null")
	add("kind-chardev-zero", "-c", "%F|%t|%T|%Hr|%Lr", "/dev/zero")
	add("kind-dir-root", "-c", "%A|%F|%n|%m", "/")
	add("kind-proc", "-c", "%F|%m", "/proc")

	// --- %N and the names it has to quote ---------------------------------
	for _, name := range []string{
		"f", "sp ace", "quo'te", "dq\"x", "tab\tx", "back\\x", "weird\xffname",
		"sl", "dangle", "link to", "weird\xfelink", "dirlink",
	} {
		add("quoted-name-"+name, "-c", "%N", name)
		add("raw-name-"+name, "-c", "%n", name)
	}
	// A modifier — ANY modifier — turns %N from the quoted form into the
	// raw one, and applies itself to the name and the link target
	// separately.
	add("name-quoted-bare", "-c", "[%N]", "dangle")
	add("name-flag-drops-quotes", "-c", "[%-N]", "dangle")
	add("name-hash-drops-quotes", "-c", "[%#N]", "dangle")
	add("name-zero-drops-quotes", "-c", "[%0N]", "f")
	add("name-plus-drops-quotes", "-c", "[%+N]", "f")
	add("name-space-drops-quotes", "-c", "[% N]", "f")
	add("name-apostrophe-drops-quotes", "-c", "[%'N]", "f")
	add("name-width-drops-quotes", "-c", "[%12N]", "sp ace")
	add("name-width-left", "-c", "[%-12N]", "sp ace")
	add("name-width-link", "-c", "[%20N]", "link to")
	add("name-prec-0", "-c", "[%.0N]", "dangle")
	add("name-prec-1", "-c", "[%.1N]", "dangle")
	add("name-prec-6", "-c", "[%.6N]", "dangle")
	add("name-prec-7", "-c", "[%.7N]", "dangle")
	add("name-prec-20", "-c", "[%.20N]", "dangle")
	add("name-prec-tab", "-c", "[%.5N]", "tab\tx")
	add("name-prec-nonutf8", "-c", "[%.7N]", "weird\xffname")
	add("name-width-prec", "-c", "[%10.3N]", "dangle")
	// GNU 9.4 writes a stray `s` after a SYMLINK's target when %N carries
	// exactly one flag other than `-`. The grid is what pins the rule: zero
	// flags, one, two and three, with and without `-` among them, and the
	// same directives against a file that is not a link, where none of it
	// happens.
	for _, spec := range []string{
		"%N", "%-N", "%--N", "%#N", "%+N", "%0N", "% N", "%'N", "%IN",
		"%##N", "%###N", "%#+N", "%0#N", "%+0N", "%##+N", "%  N",
		"%#-N", "%-#N", "%--#N", "%-- #N", "%#- -N", "%- -N", "%'-N", "%0-N",
		"%#5N", "%#-5N", "%#.5N", "%5N", "%.5N", "%I N", "%#'N",
	} {
		add("name-flag-grid-link-"+spec, "-c", "["+spec+"]", "dangle")
		add("name-flag-grid-file-"+spec, "-c", "["+spec+"]", "f")
	}
	add("name-flag-grid-real-link", "-c", "[%#N]", "sl")
	add("name-flag-grid-real-link-width", "-c", "[%#5N]", "sl")

	// --- the flag / width / precision grid --------------------------------
	// A string conversion: precision truncates, width pads, `0` and `#`
	// do nothing.
	for _, spec := range []string{
		"[%A]", "[%20A]", "[%-20A]", "[%.3A]", "[%20.3A]", "[%-20.3A]",
		"[%08A]", "[%#A]", "[%+A]", "[%.0A]", "[%.20A]", "[%1A]",
		"[%F]", "[%20F]", "[%-20F]", "[%.3F]", "[%.0F]", "[%08F]",
	} {
		add("string-grid-"+spec, "-c", spec, "f")
	}
	// An unsigned conversion: width, precision and `0`; `+` and a space
	// are ignored because the value is unsigned, and `#` reaches only the
	// octal and hex renderings.
	for _, spec := range []string{
		"[%s]", "[%8s]", "[%-8s]", "[%08s]", "[%.5s]", "[%.0s]", "[%8.5s]",
		"[%08.5s]", "[%-8.5s]", "[%+s]", "[% s]", "[%#s]", "[%'s]", "[%Is]",
		"[%-+ #08s]", "[%00005s]", "[%1s]",
	} {
		add("unsigned-grid-"+spec, "-c", spec, "f")
	}
	// `%.0u` of ZERO writes nothing at all, which only an empty file and a
	// zero mode can reach.
	add("unsigned-prec0-zero", "-c", "[%.0s]", "empty")
	add("unsigned-prec0-zero-width", "-c", "[%8.0s]", "empty")
	add("unsigned-prec0-zero-left", "-c", "[%-8.0s]", "empty")
	add("octal-grid", "-c", "[%a][%#a][%0a][%05a][%.5a][%8a][%-8a][%#08a]", "setuid")
	add("octal-grid-zero", "-c", "[%a][%#a][%05a][%.0a][%#.0a]", "zeromode")
	add("octal-grid-allperm", "-c", "[%a][%#a][%#5a]", "allperm")
	add("hex-grid", "-c", "[%D][%#D][%8D][%-8D][%08D][%.8D][%#.8D]", "f")
	add("hex-grid-zero", "-c", "[%t][%#t][%T][%#T][%.0t][%#.0t]", "f")
	add("hex-grid-dev", "-c", "[%t][%#t][%T][%#T][%R][%#R]", "/dev/null")
	add("hex-raw-mode", "-c", "[%f][%#f][%8f][%08f]", "f")
	add("hex-raw-mode-dir", "-c", "[%f][%#f]", "d")
	add("hex-raw-mode-link", "-c", "[%f][%#f]", "sl")

	// --- timestamps --------------------------------------------------------
	// The pinned files, so the calendar and the fraction are this corpus's
	// choice rather than the clock's. Local time is UTC under the harness
	// environment; the zone cases below are what prove the offset is read.
	for _, name := range []string{
		"t-epoch", "t-whole", "t-frac", "t-neg", "t-neg-frac", "t-leap", "t-split",
	} {
		add("time-"+name, "-c", "%x|%X|%y|%Y|%z|%Z", name)
	}
	for _, name := range []string{"t-epoch", "t-whole", "t-frac", "t-neg", "t-neg-frac"} {
		add("time-prec-"+name, "-c", "[%.0Y][%.1Y][%.3Y][%.9Y][%.10Y][%.20Y]", name)
	}
	add("time-sign-flags", "-c", "[%+Y][% Y][%+X][% X]", "t-whole")
	add("time-sign-flags-negative", "-c", "[%+Y][% Y]", "t-neg")
	add("time-sign-flags-prec", "-c", "[%+.3Y][% .3Y]", "t-whole")
	add("time-sign-flags-prec-negative", "-c", "[%+.3Y][% .3Y]", "t-neg-frac")
	add("time-width", "-c", "[%20Y][%-20Y][%020Y][%20.3Y][%020.3Y][%-20.3Y]", "t-frac")
	add("time-width-negative", "-c", "[%20Y][%020Y][%020.3Y]", "t-neg-frac")
	add("time-human-width", "-c", "[%40y][%-40y][%.5y][%.0y][%40.10y]", "t-frac")

	// The zone. GNU renders a timestamp in LOCAL time, so the same inode
	// reads differently under a different TZ and the offset moves with the
	// date — which is the whole of what makes these three cases worth
	// having over a fourth UTC one.
	for _, zone := range []string{
		"UTC", "America/New_York", "Asia/Kolkata", "Australia/Lord_Howe",
		"Europe/London", "Pacific/Chatham", "EST5EDT,M3.2.0,M11.1.0",
		"XXX3", "", "/etc/localtime",
	} {
		env("zone-"+zone, []string{"TZ=" + zone}, "-c", "%y|%Y|%x|%z", "t-whole")
	}
	// A summer date and a winter one on one zone: the offset the format
	// prints is the offset AT THAT INSTANT, not the zone's standard one.
	env("zone-dst-summer", []string{"TZ=America/New_York"}, "-c", "%y", "t-leap")
	env("zone-dst-winter", []string{"TZ=America/New_York"}, "-c", "%y", "t-whole")
	env("zone-prehistoric", []string{"TZ=Europe/London"}, "-c", "%y", "t-neg")

	// --- the scanner -------------------------------------------------------
	add("literal-only", "-c", "plain text", "f")
	add("empty-format", "-c", "", "f")
	add("empty-printf", "--printf", "", "f")
	add("percent-percent", "-c", "%%", "f")
	add("percent-percent-thrice", "-c", "%%%", "f")
	add("percent-trailing", "-c", "x%", "f")
	add("percent-only", "-c", "%", "f")
	add("percent-space-is-a-flag", "-c", "x% y", "f")
	add("unknown-q", "-c", "%q", "f")
	add("unknown-mid", "-c", "a%vb", "f")
	add("unknown-swallows-its-byte", "-c", "[%5]", "f")
	add("unknown-swallows-dot", "-c", "[%.]", "f")
	add("unknown-swallows-dash", "-c", "[%-]", "f")
	add("unknown-swallows-hash", "-c", "[%#]", "f")
	add("unknown-two-dots", "-c", "[%.2.3s]", "f")
	add("unknown-space-after-prec", "-c", "[%.2 5s]", "f")
	add("unknown-star-width", "-c", "[%*s]", "f")
	add("unknown-capital", "-c", "[%E]", "f")
	add("modifier-keeps-its-byte", "-c", "[%Hx]", "f")
	add("modifier-keeps-its-byte-L", "-c", "[%Ls]", "f")
	add("modifier-keeps-its-byte-n", "-c", "[%Hn]", "f")
	add("modifier-doubled", "-c", "[%HHd]", "f")
	add("modifier-doubled-L", "-c", "[%LLd]", "f")
	add("modifier-alone-at-end", "--printf", "[%H]", "f")
	add("modifier-alone-at-end-L", "--printf", "[%L]", "f")
	add("modifier-before-percent", "-c", "%H%", "f")
	add("modifier-before-percent-L", "-c", "%L%", "f")
	add("modifier-after-width", "-c", "[%-10Hd]", "f")
	add("modifier-after-zero-width", "-c", "[%010Hr]", "/dev/null")
	add("modifier-after-prec", "-c", "[%.2Ld]", "f")
	add("invalid-directive-width", "-c", "[%5%]", "f")
	add("invalid-directive-flag", "-c", "[%-%]", "f")
	add("invalid-directive-zero", "-c", "%0%", "f")
	add("invalid-directive-before-operand", "-c", "%5%", "nosuch")
	add("format-backslash-is-literal", "-c", "a\\nb\\tc", "f")
	add("format-nonutf8", "-c", "a\xffb%s", "f")

	// --- --printf escapes ---------------------------------------------------
	add("printf-newline", "--printf", "%s\\n", "f")
	add("printf-tab", "--printf", "a\\tb\\n", "f")
	add("printf-named-escapes", "--printf", "a\\ab\\bc\\ed\\fe\\rf\\vg\\n", "f")
	add("printf-backslash", "--printf", "a\\\\b\\n", "f")
	add("printf-double-quote", "--printf", "[\\\"]\\n", "f")
	add("printf-single-quote-unknown", "--printf", "[\\']\\n", "f")
	add("printf-octal", "--printf", "[\\101]\\n", "f")
	add("printf-octal-one-digit", "--printf", "[\\1]\\n", "f")
	add("printf-octal-four-digits", "--printf", "[\\1234]\\n", "f")
	add("printf-octal-wraps", "--printf", "[\\777]\\n", "f")
	add("printf-octal-zero", "--printf", "[\\0]x\\n", "f")
	add("printf-hex", "--printf", "[\\x41]\\n", "f")
	add("printf-hex-one-digit", "--printf", "[\\x4]\\n", "f")
	add("printf-hex-three-digits", "--printf", "[\\x414]\\n", "f")
	add("printf-hex-no-digits", "--printf", "[\\xz]\\n", "f")
	add("printf-hex-upper", "--printf", "[\\xFf]\\n", "f")
	add("printf-unknown-escape", "--printf", "a\\qb\\n", "f")
	add("printf-trailing-backslash", "--printf", "ab\\", "f")
	add("printf-no-trailing-newline", "--printf", "%s", "f")
	add("printf-two-operands", "--printf", "%s;", "f", "empty")
	add("printf-escape-then-directive", "--printf", "\\t%s\\t\\n", "f")
	add("format-two-operands", "-c", "%s", "f", "empty")
	add("format-three-operands", "-c", "%n", "f", "empty", "big")

	// --- -L ------------------------------------------------------------------
	add("deref-symlink", "-c", "%F|%s|%i", "sl")
	add("deref-symlink-L", "-L", "-c", "%F|%s|%i", "sl")
	add("deref-long", "--dereference", "-c", "%F|%i", "sl")
	add("deref-dir-link", "-L", "-c", "%F", "dirlink")
	add("deref-dangling", "-L", "-c", "%F", "dangle")
	add("deref-dangling-N", "-c", "%N", "dangle")
	add("deref-name-under-L", "-L", "-c", "%N", "sl")
	add("deref-twice", "-L", "-L", "-c", "%i", "sl")
	add("deref-combined", "-Lc", "%i", "sl")
	add("deref-regular-file", "-L", "-c", "%i", "f")

	// --- --cached -------------------------------------------------------------
	add("cached-always", "--cached=always", "-c", "%s", "f")
	add("cached-never", "--cached=never", "-c", "%s", "f")
	add("cached-default", "--cached=default", "-c", "%s", "f")
	add("cached-prefix-a", "--cached=a", "-c", "%s", "f")
	add("cached-prefix-n", "--cached=n", "-c", "%s", "f")
	add("cached-prefix-d", "--cached=d", "-c", "%s", "f")
	add("cached-invalid", "--cached=bogus", "-c", "%s", "f")
	add("cached-uppercase", "--cached=NEVER", "-c", "%s", "f")
	add("cached-empty", "--cached=", "-c", "%s", "f")
	add("cached-separate", "--cached", "never", "-c", "%s", "f")
	add("cached-eats-next-option", "--cached", "-c", "%s", "f")
	add("cached-missing-argument", "--cached")

	// --- the option table -----------------------------------------------------
	add("format-joined", "-c%s", "f")
	add("format-long-equals", "--format=%s", "f")
	add("format-long-separate", "--format", "%s", "f")
	add("printf-long-equals", "--printf=%s", "f")
	add("format-last-wins", "-c", "%s", "-c", "%i", "f")
	add("format-then-printf", "-c", "%s", "--printf", "%i", "f")
	add("printf-then-format", "--printf", "%i", "-c", "%s", "f")
	add("terse-then-format", "-t", "-c", "%s", "f")
	add("format-then-terse", "-c", "%s", "-t", "f")
	add("option-after-operand", "f", "-c", "%s")
	env("posixly-correct-stops-permuting", []string{"POSIXLY_CORRECT=1"}, "-c", "%s", "f")
	add("dashdash", "-c", "%n", "--", "f")
	add("dashdash-then-option-looking-operand", "-c", "%n", "--", "-c")
	add("ambiguous-f", "--f", "f")
	add("ambiguous-fo", "--fo", "f")
	add("unique-file-system", "--file-s", "-c", "%b", ".")
	add("unique-d", "--d", "-c", "%i", "sl")
	add("unique-t", "--t", "-c", "%s", "f")
	add("unique-p", "--p=%s", "f")
	add("unique-c", "--c=never", "-c", "%s", "f")
	add("invalid-option", "-Z", "f")
	add("invalid-option-p", "-p", "%s", "f")
	add("unrecognized-long", "--bogus", "f")
	add("format-missing-argument", "-c")
	add("format-long-missing-argument", "--format")
	add("printf-missing-argument", "--printf")
	add("flag-with-value", "--terse=1", "f")
	add("help-with-value", "--help=x")
	add("version-with-value", "--version=x")

	// --- operands and error paths ----------------------------------------------
	add("no-operand")
	add("missing-file", "-c", "%n", "nosuch")
	add("missing-file-default-format", "nosuch")
	add("missing-file-nonutf8", "-c", "%n", "no\xffsuch")
	add("empty-operand", "-c", "%n", "")
	add("empty-operand-only", "")
	add("missing-between-good", "-c", "%n", "f", "nosuch", "empty")
	add("missing-first", "-c", "%n", "nosuch", "f")
	add("not-a-directory", "-c", "%n", "f/")
	add("trailing-slash-on-dir", "-c", "%n|%F", "d/")
	add("dotdot", "-c", "%n|%F", "sub/..")
	add("quoting-apostrophe", "-c", "%n", "it's")
	add("quoting-space", "-c", "%n", "no such")
	add("quoting-tab", "-c", "%n", "tab\tmissing")
	add("stdin-operand", "-c", "%n|%F", "-")
	add("stdin-operand-twice", "-c", "%n", "-", "-")
	add("stdin-after-dashdash", "-c", "%n", "--", "-")

	// --- %m --------------------------------------------------------------------
	add("mount-point-file", "-c", "%m", "f")
	add("mount-point-dir", "-c", "%m", "sub")
	add("mount-point-relative", "-c", "%m", "sub/../f")
	add("mount-point-root", "-c", "%m", "/")
	add("mount-point-root-dot", "-c", "%m", "/.")
	add("mount-point-dev", "-c", "%m", "/dev/null")
	add("mount-point-proc", "-c", "%m", "/proc/self")
	add("mount-point-symlink", "-c", "%m", "sl")
	add("mount-point-symlink-deref", "-L", "-c", "%m", "sl")
	add("mount-point-fails-on-stdin", "-c", "%m", "-")
	add("mount-point-width", "-c", "[%10m][%-10m][%.2m]", "f")

	// --- the file system mode ---------------------------------------------------
	// Only the fields that do not move between the two runs: the total
	// block and inode counts, the block size and the name length. The
	// three free counters are what a busy machine changes underneath the
	// corpus, so they appear only where the point is the SCANNER.
	add("fs-block-size", "-f", "-c", "%s", ".")
	// %a, %d and %f are absent by design: see statTree's header.
	add("fs-blocks-total", "-f", "-c", "%b", ".")
	add("fs-inodes-total", "-f", "-c", "%c", ".")
	add("fs-namelen", "-f", "-c", "%l", ".")
	add("fs-name", "-f", "-c", "%n", ".")
	add("fs-stable-set", "-f", "-c", "%n|%l|%s|%b|%c", ".")
	add("fs-long-option", "--file-system", "-c", "%b", ".")
	add("fs-root", "-f", "-c", "%b|%c|%l|%s", "/")
	add("fs-file-operand", "-f", "-c", "%b|%l", "f")
	add("fs-dev", "-f", "-c", "%l|%n", "/dev/null")
	add("fs-two-operands", "-f", "-c", "%n|%l", ".", "/")
	add("fs-missing", "-f", "-c", "%n", "nosuch")
	add("fs-missing-nonutf8", "-f", "-c", "%n", "no\xffsuch")
	add("fs-empty-operand", "-f", "-c", "%n", "")
	add("fs-stdin-refused", "-f", "-c", "%n", "-")
	add("fs-stdin-refused-among-others", "-f", "-c", "%n", ".", "-", "/")
	add("fs-deref", "-f", "-L", "-c", "%s", "sl")
	add("fs-widths", "-f", "-c", "[%10l][%-10l][%.6l][%08l]", ".")
	// The scanner in file-system mode: `H` and `L` are not modifiers here
	// at all, so the byte after one comes back as literal text, and the
	// directives that belong to the OTHER alphabet are unknown.
	add("fs-unknown-A", "-f", "-c", "[%A]", ".")
	add("fs-unknown-N", "-f", "-c", "[%N]", ".")
	add("fs-unknown-z", "-f", "-c", "[%z]", ".")
	add("fs-unknown-x", "-f", "-c", "[%x]", ".")
	add("fs-modifier-keeps-its-byte", "-f", "-c", "[%Hd]", ".")
	add("fs-modifier-keeps-its-byte-a", "-f", "-c", "[%Ha]", ".")
	add("fs-modifier-L", "-f", "-c", "[%Ld]", ".")
	add("fs-percent-percent", "-f", "-c", "%%", ".")
	add("fs-invalid-directive", "-f", "-c", "%5%", ".")
	add("fs-printf", "-f", "--printf", "%b\\n", ".")
	add("fs-printf-no-newline", "-f", "--printf", "%l", ".")

	// --- write failures -----------------------------------------------------------
	cases = append(cases, invocation{
		name: "write-error-full", args: []string{"-c", "%n", "f"}, dir: dir, stdout: stdoutFull,
	})
	cases = append(cases, invocation{
		name: "write-error-closed", args: []string{"-c", "%n", "f"}, dir: dir, stdout: stdoutClosed,
	})
	cases = append(cases, invocation{
		name: "write-error-closed-usage", args: []string{}, dir: dir, stdout: stdoutClosed,
	})
	cases = append(cases, invocation{
		name: "write-error-full-many", args: []string{"-c", "%n|%s|%A|%y", "f", "empty", "big", "d"}, dir: dir, stdout: stdoutFull,
	})
	return cases
}

func TestStat(t *testing.T) {
	requireParity(t, "stat", statCases(t))
}

func TestStatHelp(t *testing.T) {
	requireHelp(t, "stat", []string{"--help"}, 0)
	requireHelp(t, "stat", []string{"--hel"}, 0)
	requireHelp(t, "stat", []string{"f", "--help"}, 0)
}

func TestStatVersion(t *testing.T) {
	requireVersion(t, "stat", []string{"--version"}, 0)
	requireVersion(t, "stat", []string{"--vers"}, 0)
	requireVersion(t, "stat", []string{"-t", "--version"}, 0)
}
