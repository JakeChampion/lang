package coreutils

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("stat", statCases)
}

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
	link("/dev/null", j("devlink"))
	if err := os.Link(j("f"), j("hard")); err != nil {
		t.Fatal(err)
	}
	seedFifo(t, j("fifo"))

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

	stamp := func(path string, sec int64, nsec int64) {
		t.Helper()
		write(path, 0)
		tv := []syscall.Timespec{{Sec: sec, Nsec: nsec}, {Sec: sec, Nsec: nsec}}
		if err := syscall.UtimesNano(path, tv); err != nil {
			t.Fatal(err)
		}
	}
	stamp(j("t-epoch"), 0, 0)
	stamp(j("t-epoch-frac"), 0, 123456789)
	stamp(j("t-whole"), 981173106, 0)
	stamp(j("t-frac"), 981173106, 123456789)
	stamp(j("t-neg"), -315521755, 0)
	stamp(j("t-neg-frac"), -315521756, 500000000)
	stamp(j("t-leap"), 1709164800, 999999999)
	if err := os.Chtimes(j("t-split"), time.Unix(1000000000, 111111111), time.Unix(2000000000, 222222222)); err != nil {
		write(j("t-split"), 0)
		if err := os.Chtimes(j("t-split"), time.Unix(1000000000, 111111111), time.Unix(2000000000, 222222222)); err != nil {
			t.Fatal(err)
		}
	}

	write(j("sp ace"), 1)
	write(j("quo'te"), 1)
	write(j("dq\"x"), 1)
	write(j("tab\tx"), 1)
	write(j("back\\x"), 1)
	if rawByteNamesHeld(t) {
		write(j("weird\xffname"), 1)
	}
	link("sp ace", j("link to"))
	if rawByteNamesHeld(t) {
		link("weird\xffname", j("weird\xfelink"))
	}
	write(j("col:on"), 1)
	write(j("{"), 1)
	write(j("a{b}"), 1)
	write(j("quo'te@"), 1)
	write(j("eq=ual"), 1)
	write(j("bell\ax"), 1)
	write(j("del\x7fx"), 1)
	write(j("new\nline"), 1)
	write(j("ctl\x01x"), 1)
	write(j("bs\\dq\""), 1)
	link("quo'te@", j("link'to"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			if _, err := os.Readlink(j(e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func appendRawStatNames(t *testing.T, names []string) []string {
	t.Helper()
	if !rawByteNamesHeld(t) {
		return names
	}
	return append(names, "weird\xffname", "weird\xfelink")
}

func statCases(t *testing.T) []invocation {
	dir := statTree(t)
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir})
	}
	env := func(name string, envs []string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, dir: dir, env: envs})
	}

	for _, spec := range []string{
		"%a", "%A", "%b", "%B", "%d", "%D", "%f", "%F", "%g", "%G", "%h",
		"%i", "%m", "%n", "%N", "%o", "%r", "%R", "%s", "%t", "%T", "%u",
		"%U", "%w", "%W", "%x", "%X", "%y", "%Y", "%z", "%Z", "%Hd", "%Ld", "%Hr", "%Lr",
	} {
		add("file-directive-"+spec, "-c", spec, "f")
	}
	add("file-all-directives", "-c", "%a %b %B %d %D %f %g %h %i %o %r %R %s %t %T %u", "f")
	add("file-all-strings", "-c", "%A|%F|%G|%U|%n|%N|%m", "f")

	for _, name := range []string{
		"f", "empty", "big", "d", "sub", "sl", "dangle", "dirlink", "hard",
		"fifo", "setuid", "setgid", "nox-setuid", "nox-setgid", "zeromode",
		"allperm", "sticky", "sticky-nox", "setuid-setgid-sticky",
	} {
		add("kind-"+name, "-c", "%A|%a|%#a|%f|%F|%h|%s", name)
	}
	add("kind-chardev", "-c", "%A|%F|%f|%r|%R|%t|%T|%Hr|%Lr|%Hd|%Ld", "/dev/null")

	add("birth-grid", "-c", "[%w][%W][%.3W][%.9W][%-12W][%30w][%-30w]", "f")
	for _, name := range []string{"t-epoch", "t-epoch-frac", "t-neg", "t-neg-frac"} {
		add("birth-"+name, "-c", "[%w][%W][%.3W][%.9W]", name)
	}
	add("birth-link", "-c", "%w|%W", "sl")
	add("birth-link-deref", "-L", "-c", "%w|%W", "sl")
	for _, name := range []string{"f", "empty", "d", "sl", "dangle", "fifo", "hard", "setuid-setgid-sticky"} {
		add("default-"+name, name)
		add("terse-"+name, "-t", name)
	}
	add("default-chardev", "-c", "%n|%s|%b|%o|%F|%Hd,%Ld|%i|%h|%Hr,%Lr|%a|%A|%u|%U|%g|%G", "/dev/null")
	add("terse-chardev", "-c", "%n %s %b %f %u %g %D %i %h %t %T %W %o", "/dev/null")
	add("default-deref", "-L", "sl")
	add("terse-deref", "-t", "-L", "sl")
	add("default-two", "f", "d")
	add("terse-two", "-t", "f", "d")
	add("terse-two-chardev", "-c", "%n %s %b %f %u %g %D %i %h %t %T %W %o", "f", "/dev/null")
	add("default-missing-between", "f", "nosuch", "d")
	add("terse-missing", "-t", "nosuch")
	add("kind-chardev-zero", "-c", "%F|%t|%T|%Hr|%Lr", "/dev/zero")
	add("kind-dir-root", "-c", "%A|%F|%n|%m", "/")
	add("kind-proc", "-c", "%F|%m", "/proc")

	for _, name := range appendRawStatNames(t, []string{
		"f", "sp ace", "quo'te", "dq\"x", "tab\tx", "back\\x",
		"sl", "dangle", "link to", "dirlink",
	}) {
		add("quoted-name-"+name, "-c", "%N", name)
		add("raw-name-"+name, "-c", "%n", name)
	}
	quotedNames := appendRawStatNames(t, []string{
		"f", "sp ace", "quo'te", "dq\"x", "tab\tx", "back\\x",
		"sl", "dangle", "link to", "dirlink", "link'to",
		"col:on", "{", "a{b}", "quo'te@", "eq=ual", "bell\ax", "del\x7fx",
		"new\nline", "ctl\x01x", "bs\\dq\"",
	})
	for _, style := range []string{
		"literal", "shell", "shell-always", "shell-escape", "shell-escape-always",
		"c", "c-maybe", "escape", "locale", "clocale",
	} {
		for _, name := range quotedNames {
			env("style-"+style+"-"+name, []string{"QUOTING_STYLE=" + style}, "-c", "%N", name)
		}
	}
	for _, style := range []string{"lit", "c-m", "she", "shell-e", "", "bo'g", "bogus", "a\nb"} {
		env("style-"+style+"-plain", []string{"QUOTING_STYLE=" + style}, "-c", "%N", "f")
		env("style-"+style+"-quote", []string{"QUOTING_STYLE=" + style}, "-c", "%N", "quo'te")
	}
	bogus := []string{"QUOTING_STYLE=bogus"}
	env("bogus-unread-n", bogus, "-c", "%n", "f")
	env("bogus-unread-raw-N", bogus, "-c", "%-N", "f")
	env("bogus-unread-octal-percent", bogus, "--printf", "\\045N\\n", "f")
	env("bogus-read-percent-percent-N", bogus, "-c", "%%N", "f")
	env("bogus-before-missing-file", bogus, "-c", "%N", "nowhere")
	env("bogus-fs-mode", bogus, "-f", "-c", "%N", "f")
	env("bogus-once-for-two", bogus, "--printf", "%N\\n", "f", "sl")
	env("bogus-trailing-text", bogus, "-c", "%Nz", "f")
	env("bogus-last-format-wins", bogus, "-c", "%n", "--printf", "%N\\n", "f")
	env("bogus-after-missing-operand", bogus, "-c", "%N")
	env("bogus-unread-fs-n", bogus, "-f", "-c", "%n", "f")
	env("bogus-read-fs-Qn", bogus, "-f", "-c", "%Qn", "f")
	env("bogus-read-Qn", bogus, "-c", "%Qn", "f")
	env("bogus-unread-Q-alone", bogus, "-c", "%Q", "f")
	env("bogus-N-then-missing", bogus, "--printf", "%N\n", "f", "nowhere")
	env("bogus-missing-then-N", bogus, "--printf", "%N\n", "nowhere", "f")

	for _, spec := range []string{"%Qn", "%-Qn", "%12Qn", "%-12Qn", "%.3Qn", "%Q", "%QQn", "%HQn", "%QHn", "%Qs", "%QN", "%Qd", "%Qr", "%Qz"} {
		add("mod-Q-"+spec, "-c", "["+spec+"]", "sp ace")
		add("mod-Q-fs-"+spec, "-f", "-c", "["+spec+"]", "f")
	}
	add("mod-Q-link", "-c", "[%Qn]", "sl")
	add("mod-Q-plain", "-c", "[%Qn]", "f")

	add("name-quoted-bare", "-c", "[%N]", "dangle")
	add("name-flag-minus", "-c", "[%-N]", "dangle")
	add("name-flag-hash", "-c", "[%#N]", "dangle")
	add("name-flag-zero", "-c", "[%0N]", "f")
	add("name-flag-plus", "-c", "[%+N]", "f")
	add("name-flag-space", "-c", "[% N]", "f")
	add("name-flag-apostrophe", "-c", "[%'N]", "f")
	add("name-width-quoted", "-c", "[%12N]", "sp ace")
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

	for _, spec := range []string{
		"[%A]", "[%20A]", "[%-20A]", "[%.3A]", "[%20.3A]", "[%-20.3A]",
		"[%08A]", "[%#A]", "[%+A]", "[%.0A]", "[%.20A]", "[%1A]",
		"[%F]", "[%20F]", "[%-20F]", "[%.3F]", "[%.0F]", "[%08F]",
	} {
		add("string-grid-"+spec, "-c", spec, "f")
	}
	for _, spec := range []string{
		"[%s]", "[%8s]", "[%-8s]", "[%08s]", "[%.5s]", "[%.0s]", "[%8.5s]",
		"[%08.5s]", "[%-8.5s]", "[%+s]", "[% s]", "[%#s]", "[%'s]", "[%Is]",
		"[%-+ #08s]", "[%00005s]", "[%1s]",
	} {
		add("unsigned-grid-"+spec, "-c", spec, "f")
	}
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

	for _, zone := range []string{
		"UTC", "America/New_York", "Asia/Kolkata", "Australia/Lord_Howe",
		"Europe/London", "Pacific/Chatham", "EST5EDT,M3.2.0,M11.1.0",
		"XXX3", "", "/etc/localtime",
	} {
		env("zone-"+zone, []string{"TZ=" + zone}, "-c", "%y|%Y|%x|%z", "t-whole")
	}
	env("zone-dst-summer", []string{"TZ=America/New_York"}, "-c", "%y", "t-leap")
	env("zone-dst-winter", []string{"TZ=America/New_York"}, "-c", "%y", "t-whole")
	env("zone-prehistoric", []string{"TZ=Europe/London"}, "-c", "%y", "t-neg")

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
	add("printf-warns-per-operand", "--printf", "[\\q]\\n", "f", "sl")
	add("printf-warns-after-missing", "--printf", "[\\q]", "nowhere", "f")
	add("printf-escaped-percent", "--printf", "\\%s|\\n", "f")
	add("printf-backslash-then-directive", "--printf", "\\\\%n\\n", "f")
	add("printf-octal-percent", "--printf", "\\045n\\n", "f")
	add("printf-hex-warns-then-directive", "--printf", "[\\xz]%n\\n", "f")
	add("format-keeps-backslash", "-c", "%n\\q", "f")
	add("printf-unknown-escape", "--printf", "a\\qb\\n", "f")
	add("printf-trailing-backslash", "--printf", "ab\\", "f")
	add("printf-no-trailing-newline", "--printf", "%s", "f")
	add("printf-two-operands", "--printf", "%s;", "f", "empty")
	add("printf-escape-then-directive", "--printf", "\\t%s\\t\\n", "f")
	add("format-two-operands", "-c", "%s", "f", "empty")
	add("format-three-operands", "-c", "%n", "f", "empty", "big")

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

	add("fs-block-size", "-f", "-c", "%s", ".")
	add("fs-blocks-total", "-f", "-c", "%b", ".")
	add("fs-namelen", "-f", "-c", "%l", ".")
	add("fs-name", "-f", "-c", "%n", ".")
	add("fs-stable-set", "-f", "-c", "%n|%l|%s|%b", ".")
	add("fs-long-option", "--file-system", "-c", "%b", ".")
	add("fs-root", "-f", "-c", "%b|%l|%s", "/")
	add("fs-file-operand", "-f", "-c", "%b|%l", "f")
	add("fs-dev", "-f", "-c", "%l|%n", "/dev/null")
	add("fs-dev-geometry", "-f", "-c", "%s|%l|%n", "/dev/null")
	add("fs-dev-link", "-f", "-c", "%s|%l|%n", "devlink")
	add("fs-two-operands", "-f", "-c", "%n|%l", ".", "/")
	add("fs-missing", "-f", "-c", "%n", "nosuch")
	add("fs-missing-nonutf8", "-f", "-c", "%n", "no\xffsuch")
	add("fs-empty-operand", "-f", "-c", "%n", "")
	add("fs-stdin-refused", "-f", "-c", "%n", "-")
	add("fs-stdin-refused-among-others", "-f", "-c", "%n", ".", "-", "/")
	add("fs-deref", "-f", "-L", "-c", "%s", "sl")
	add("fs-widths", "-f", "-c", "[%10l][%-10l][%.6l][%08l]", ".")
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
	add("fs-id", "-f", "-c", "%i", ".")
	add("fs-frag-size", "-f", "-c", "%S", ".")
	add("fs-type-hex", "-f", "-c", "%t", ".")
	add("fs-new-set-root", "-f", "-c", "%i|%S|%t", "/")
	add("fs-hex-grid", "-f", "-c", "[%#t][%10t][%-10i][%#i][%.12i][%08S]", ".")
	add("fs-type-name", "-f", "-c", "%T", ".")
	add("fs-type-name-widths", "-f", "-c", "[%12T][%-12T][%.3T]", ".")
	add("fs-type-name-dev", "-f", "-c", "%t|%T", "/dev")
	stableFS := "/dev"
	if runtime.GOOS == "linux" {
		stableFS = "/proc"
	}
	add("fs-default", "-f", stableFS)
	add("fs-terse", "-t", "-f", stableFS)
	add("fs-terse-two", "-t", "-f", stableFS, stableFS)
	add("fs-default-missing", "-f", "nosuch")
	if runtime.GOOS == "linux" {
		add("fs-type-name-proc", "-f", "-c", "%t|%T", "/proc")
		add("birth-none", "-c", "%w|%W|%.3W", "/proc")
		add("default-none", "/proc/version")
		add("terse-none", "-t", "/proc/version")
	}

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
