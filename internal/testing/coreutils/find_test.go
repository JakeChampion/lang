package coreutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The corpus of the find port. GNU findutils 4.9.0 is the oracle: each case
// runs on both sides and the streams, the status and, for a case that
// changes files, the tree must match. The cases come from the documented
// behaviour of GNU find and from runs of the GNU binary. No case is copied
// from the GNU test suite.
//
// The order of a walk is the order of readdir. Most cases read one shared
// tree, so both sides see the same directories in the same order. A case
// that changes files gets a fresh tree for each side, made by the same
// steps, so readdir gives the same order on the same file system.

func init() {
	registerCorpus("find", findCases)
}

func findCases(t *testing.T) []invocation {
	shared := findSharedTree(t)
	many := findManyTree(t)
	var cases []invocation
	cases = append(cases, findLines(shared, findTestLines)...)
	cases = append(cases, findLines(shared, findPrintfLines)...)
	cases = append(cases, findLines(shared, findTimeLines)...)
	cases = append(cases, findLines(shared, findSymlinkLines)...)
	cases = append(cases, findLines(shared, findExecLines)...)
	cases = append(cases, findLines(shared, findSyntaxLines)...)
	cases = append(cases, findLines(shared, findStartLines)...)
	cases = append(cases, findInodeCases(shared)...)
	cases = append(cases, findOkCases(shared)...)
	cases = append(cases, findChangeCases()...)
	cases = append(cases, findOutputCases(shared)...)
	cases = append(cases, findLines(many, findManyLines)...)
	handOver(t, shared)
	handOver(t, many)
	for i := range cases {
		cases[i].unprivileged = true
	}
	return cases
}

// shellWords splits a case line as a shell would: blanks separate words,
// single quotes keep everything, double quotes keep all but a backslash
// before a double quote or a backslash, and a backslash outside quotes
// keeps the next byte.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			in = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				panic("unclosed quote in case " + s)
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			in = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					i++
				}
				cur.WriteByte(s[i])
			}
		case c == '\\' && i+1 < len(s):
			in = true
			i++
			cur.WriteByte(s[i])
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			in = true
			cur.WriteByte(c)
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}

func findLines(dir string, lines []string) []invocation {
	var cases []invocation
	for _, line := range lines {
		cases = append(cases, invocation{name: "find " + line, args: shellWords(line), dir: dir, darwinLibcDefect: findDarwinDefects[line]})
	}
	return cases
}

// findDarwinDefects are the lines whose Darwin reference is wrong. GNU find
// gives these directives to the printf and the strftime of the libc.
var findDarwinDefects = map[string]string{
	`-printf '%5m|%-5s|%05s|%+d|%#m|%.2f|%10p|%-10f|\n'`:                                                     "Apple's printf pads a string with zeros under the 0 flag, where glibc pads it with spaces",
	`-printf '%Aq|%AE|%AO|%AC|%Ae|%AF|%Ag|%AG|%Ah|%An|%AP|%AR|%As|%At|%Au|%AV|%Az|%A%|%A1|%AN\n' -name frac`: "Apple's strftime writes an unknown conversion as its letter alone, and it has no %P",
}

// Each line of findDarwinDefects must be a line of the corpus, or its reason
// marks nothing.
func TestFindDarwinDefectsNameCorpusLines(t *testing.T) {
	all := map[string]bool{}
	for _, lines := range [][]string{findTestLines, findPrintfLines, findTimeLines, findSymlinkLines, findExecLines, findSyntaxLines, findStartLines, findManyLines} {
		for _, line := range lines {
			all[line] = true
		}
	}
	for line := range findDarwinDefects {
		if !all[line] {
			t.Errorf("findDarwinDefects names %q, which is not a line of the find corpus", line)
		}
	}
}

func mkdirAll(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func fileWith(t *testing.T, dir, name string, size int, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func symlinks(t *testing.T, dir string, pairs ...string) {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		if err := os.Symlink(pairs[i+1], filepath.Join(dir, pairs[i])); err != nil {
			t.Fatal(err)
		}
	}
}

// findBasicTree makes the files that every tree of the corpus has: each
// kind of file, names with special bytes, links and a loop of links.
func findBasicTree(t *testing.T, dir string) {
	t.Helper()
	fileWith(t, dir, "empty", 0, 0o644)
	fileWith(t, dir, "small", 100, 0o644)
	fileWith(t, dir, "k1", 1024, 0o644)
	fileWith(t, dir, "exe", 20, 0o755)
	fileWith(t, dir, "sub/a.c", 7, 0o644)
	fileWith(t, dir, "sub/b.h", 0, 0o644)
	fileWith(t, dir, "sub/.hidden", 1, 0o644)
	fileWith(t, dir, "sub/deep/er/est.c", 1, 0o644)
	fileWith(t, dir, "UPPER.C", 1, 0o644)
	fileWith(t, dir, "sp ace", 1, 0o644)
	fileWith(t, dir, "nl\nx", 1, 0o644)
	fileWith(t, dir, "q\"uote", 1, 0o644)
	fileWith(t, dir, "back\\slash", 1, 0o644)
	fileWith(t, dir, "tab\tx", 1, 0o644)
	fileWith(t, dir, "-dash", 1, 0o644)
	fileWith(t, dir, "star*", 1, 0o644)
	seedRawByteName(t, dir, "raw")
	mkdirAll(t, dir, "emptydir", "sub/emptysub")
	if err := os.Link(filepath.Join(dir, "small"), filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	symlinks(t, dir, "lsmall", "small", "lsub", "sub", "dangling", "nowhere", "selfloop", "selfloop", "sub/up", "..", "lexe", "exe")
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedSocket(t, filepath.Join(dir, "sock"))
}

// findSharedTree makes the tree that the cases which change nothing read.
// The times are fixed, apart from the ones that measure the age of a file
// from now; those are far from each boundary of a day and a minute.
func findSharedTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	findBasicTree(t, dir)
	now := time.Now()
	day := 24 * time.Hour
	fileWith(t, dir, "k10", 10240, 0o644)
	fileWith(t, dir, "big", 1048577, 0o644)
	fileWith(t, dir, "suid", 0, 0o4755)
	fileWith(t, dir, "sgid", 0, 0o2711)
	fileWith(t, dir, "sticky_file", 0, 0o1644)
	fileWith(t, dir, "none", 0, 0o000)
	fileWith(t, dir, "wo", 0, 0o222)
	mkdirAll(t, dir, "sticky", "locked/inner")
	if err := os.Chmod(filepath.Join(dir, "sticky"), 0o1777); err != nil {
		t.Fatal(err)
	}
	times := map[string][2]time.Time{
		"old":    {time.Unix(1000000000, 0), time.Unix(1000000000, 0)},
		"mid":    {now.Add(-3*day - time.Hour), now.Add(-3*day - time.Hour)},
		"recent": {now.Add(-90 * time.Minute), now.Add(-90 * time.Minute)},
		"future": {now.Add(10 * day), now.Add(10 * day)},
		"aold":   {now.Add(-40 * day), now.Add(-100 * time.Second)},
		"frac":   {time.Unix(1700000000, 123456789), time.Unix(1700000000, 987654321)},
	}
	for name, at := range times {
		p := fileWith(t, dir, name, 3, 0o644)
		if err := os.Chtimes(p, at[0], at[1]); err != nil {
			t.Fatal(err)
		}
	}
	if os.Geteuid() == 0 {
		_ = os.Chown(filepath.Join(dir, "old"), 54321, 54321)
		_ = os.Chown(filepath.Join(dir, "mid"), 0, 54322)
		_ = syscall.Mknod(filepath.Join(dir, "cdev"), syscall.S_IFCHR|0o644, 1<<8|3)
	}
	if err := os.Chmod(filepath.Join(dir, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })
	pinDirAtimes(t, dir, now.Add(time.Hour))
	return dir
}

// pinDirAtimes sets the access time of each directory a little after now.
// A walk reads each directory, and a later access time keeps relatime from
// changing it, so %A and -used of a directory are the same on both sides.
func pinDirAtimes(t *testing.T, dir string, at time.Time) {
	t.Helper()
	var dirs []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for _, d := range dirs {
		info, err := os.Lstat(d)
		if err != nil {
			continue
		}
		_ = os.Chtimes(d, at, info.ModTime())
	}
}

// findManyTree holds a directory of more than 10000 entries, which gnulib's
// fts sorts by inode number on most file systems.
func findManyTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkdirAll(t, dir, "many", "few")
	for i := 0; i < 10050; i++ {
		writeFile(t, filepath.Join(dir, "many"), fmt.Sprintf("f%05d", i), "")
	}
	for i := 0; i < 50; i++ {
		writeFile(t, filepath.Join(dir, "few"), fmt.Sprintf("g%02d", i), "")
	}
	pinDirAtimes(t, dir, time.Now().Add(time.Hour))
	return dir
}

// findChangeTree is the tree of the cases that delete or write files.
func findChangeTree(t *testing.T, dir string) {
	t.Helper()
	findBasicTree(t, dir)
	mkdirAll(t, dir, "full/one/two", "keep")
	writeFile(t, filepath.Join(dir, "keep"), "k", "")
}

var findTestLines = []string{
	``,
	`.`,
	`-print`,
	`-print0`,
	`-name '*.c'`,
	`-name '*.C'`,
	`-iname '*.c'`,
	`-name '*.[ch]'`,
	`-name '[!s]*'`,
	`-name '[^s]*'`,
	`-name '[[:upper:]]*'`,
	`-name '\*'`,
	`-name 'star\*'`,
	`-name 's?all'`,
	`-name '.*'`,
	`-name '*'`,
	`-name 'sub'`,
	`-name '[a-'`,
	`-name 'a\'`,
	`-iname 'UP*'`,
	`-iname '[u]pper.c'`,
	`-path './sub/*'`,
	`-path '*/deep/*'`,
	`-path './sub'`,
	`-ipath './SUB/*.C'`,
	`-wholename './sub/a.c'`,
	`-iwholename './Sub/A.C'`,
	`-regex '.*/[ab]\.[ch]'`,
	`-regex '.*\(old\|mid\)'`,
	`-regex '.*/s.*'`,
	`-regex 'sub'`,
	`-iregex '.*/upper\.c'`,
	`-regextype posix-extended -regex '.*/(old|mid)'`,
	`-regextype posix-basic -regex '.*/\(old\|mid\)'`,
	`-regextype egrep -regex '.*/[[:digit:]]+'`,
	`-regextype sed -regex '.*/k[0-9]\{1,2\}'`,
	`-regextype posix-extended -iregex '.*/K[0-9]{2}'`,
	`-regextype grep -regex '.*/o.d'`,
	`-regextype awk -regex '.*/(old)'`,
	`-type f`,
	`-type d`,
	`-type l`,
	`-type p`,
	`-type s`,
	`-type c`,
	`-type b`,
	`-type f,d`,
	`-type l,p,s`,
	`-type D`,
	`-xtype f`,
	`-xtype l`,
	`-xtype d`,
	`-empty`,
	`-empty -type d`,
	`-size 0`,
	`-size -1`,
	`-size 1`,
	`-size +1`,
	`-size 100c`,
	`-size -100c`,
	`-size +99c`,
	`-size 1k`,
	`-size 10k`,
	`-size +10k`,
	`-size -10k`,
	`-size 20`,
	`-size 1M`,
	`-size +1M`,
	`-size 2M`,
	`-size 1G`,
	`-size 50w`,
	`-size 2b`,
	`-perm 644`,
	`-perm -644`,
	`-perm /4000`,
	`-perm -4000`,
	`-perm /6000`,
	`-perm -u+s`,
	`-perm /u+s,g+s`,
	`-perm -1000`,
	`-perm 0`,
	`-perm -0`,
	`-perm /0`,
	`-perm u=rw,g=r,o=r`,
	`-perm -a+r`,
	`-perm /o+w`,
	`-perm g+s`,
	`-perm -g=s`,
	`-perm 2711`,
	`-perm +x`,
	`-perm /222`,
	`-perm -o=w`,
	`-links 1`,
	`-links 2`,
	`-links +1`,
	`-links -2`,
	`-uid 0`,
	`-uid +0`,
	`-uid -1`,
	`-gid 0`,
	`-uid 54321`,
	`-gid 54322`,
	`-user root`,
	`-user 0`,
	`-user 54321`,
	`-group root`,
	`-group 0`,
	`-group 54322`,
	`-nouser`,
	`-nogroup`,
	`-readable`,
	`-writable`,
	`-executable`,
	`-fstype nosuchfs`,
	`-true`,
	`-false`,
	`! -name '*.c'`,
	`-not -type f`,
	`-name old -o -name mid`,
	`-name old -or -name mid -a -type f`,
	`-name old , -name mid`,
	`'(' -name old -o -name mid ')' -print`,
	`-type f -name 'k*' -o -type d -name 's*'`,
	`! '(' -type f -o -type d ')'`,
	`-name sub -prune`,
	`-name sub -prune -o -print`,
	`-depth -name sub -prune -o -print`,
	`-name sub -prune , -name a.c`,
	`-quit`,
	`-name small -quit`,
	`-print -quit`,
	`-name mid -print -quit -print`,
	`-maxdepth 0`,
	`-maxdepth 1`,
	`-maxdepth 2 -mindepth 2`,
	`-mindepth 1 -maxdepth 1 -name 's*'`,
	`-mindepth 4`,
	`-depth`,
	`-depth -maxdepth 1`,
	`-d -maxdepth 1`,
	`-depth -mindepth 2`,
	`-xdev -maxdepth 1`,
	`-mount -maxdepth 1`,
	`-noleaf -maxdepth 1`,
	`-ignore_readdir_race -maxdepth 1`,
	`-noignore_readdir_race -maxdepth 1`,
	`-follow -maxdepth 1`,
	`-samefile small`,
	`-samefile hardlink`,
	`-samefile lsmall`,
	`-L -samefile lsmall`,
	`-samefile nosuch`,
	`-lname 'sm*'`,
	`-lname '*'`,
	`-ilname 'SM*'`,
	`-lname '..'`,
	`-L -lname '*'`,
}

var findPrintfLines = []string{
	`-ls`,
	`-printf '%p\n'`,
	`-printf '[%h][%f][%P][%H][%d]\n'`,
	`-printf '%m %M %n %U %G %u %g %s %k %b\n'`,
	`-printf '%5m|%-5s|%05s|%+d|%#m|%.2f|%10p|%-10f|\n'`,
	`-printf '%y%Y %p\n'`,
	`-printf '%l|%p\n'`,
	`-printf '%S %p\n'`,
	`-printf '%.3S|%10.2S|%-8S|%#S|%+S\n'`,
	`-printf '%F %D\n' -maxdepth 0`,
	`-printf '%i %p\n' -name small`,
	`-printf '%t|%a\n' -name old`,
	`-printf '%c|%Cs\n' -maxdepth 0`,
	`-printf '%Ta %Tb %Td %TH %Tj %Tm %TM %TS %TU %Tw %TW %Ty %TY %TZ %T+ %T@ %Tk %Tl %Tp %Tr %TT %TX %Tx %TD %Th %TA %TB %Tc %TI\n' -name old`,
	`-printf '%A+ %A@ %AS %AT\n' -name frac`,
	`-printf '%T+ %T@ %TS %TT %TX %Tc\n' -name frac`,
	`-printf '%Aq|%AE|%AO|%AC|%Ae|%AF|%Ag|%AG|%Ah|%An|%AP|%AR|%As|%At|%Au|%AV|%Az|%A%|%A1|%AN\n' -name frac`,
	`-printf '%B+|%Bs|%BT\n' -name frac`,
	`-printf '%-10T@|%30T+|\n' -name frac`,
	`-printf '%Q'`,
	`-printf '\q'`,
	`-printf '%A'`,
	`-printf 'abc%'`,
	`-printf '%-'`,
	`-printf '%5'`,
	`-printf '%5.3'`,
	`-printf '\0\1\12\101\777|\a\b\f\n\r\t\v\\|\c|after' -name old`,
	`-printf '%5%|%-5d|%05d|% d|%x\n' -name old`,
	`-printf '%f\n' -name 'sp*'`,
	`-printf '%u %g %U %G\n' -name old`,
	`-printf '%u %g\n' -name mid`,
	`-printf '%Z\n' -name small`,
	`-printf '%h/%f|%-3d|\n' -maxdepth 2`,
	`-printf ''`,
	`-printf 'x' -printf 'y\n' -name small`,
	`-printf '%%\n' -name small`,
	`-printf '%y\n' -name fifo -o -name sock -printf '%y\n'`,
	`-name old -ls`,
	`-name future -ls`,
	`-name fifo -ls`,
	`-name sock -ls`,
	`-name sticky -ls`,
	`-name dangling -ls`,
	`-name cdev -ls`,
	`-L -name dangling -ls`,
	`-name 'nl*' -ls`,
	`-name 'q*' -ls`,
	`-name 'back*' -ls`,
	`-name 'tab*' -ls`,
	`-name 'sp*' -ls`,
	`-name 'raw*' -ls`,
	`-name 'raw*' -printf '%f|%p|%P\n'`,
	`-name 'raw*' -print0`,
	`-name 'raw*' -name '*w*' -print`,
}

var findTimeLines = []string{
	`-mtime 0`,
	`-mtime +0`,
	`-mtime -1`,
	`-mtime 3`,
	`-mtime +2`,
	`-mtime -4`,
	`-mtime +1000`,
	`-mtime 1.5`,
	`-mtime -0.5`,
	`-mtime +-1`,
	`-mtime 1e0`,
	`-mmin -120`,
	`-mmin +60`,
	`-name recent -mmin 91`,
	`-name recent -mmin -91`,
	`-name recent -mmin +90`,
	`-amin -60`,
	`-atime +30`,
	`-atime 40`,
	`-atime -41`,
	`-ctime 0`,
	`-ctime +0`,
	`-cmin -60`,
	`-cmin +60`,
	`-used 0`,
	`-used +0`,
	`-used -1`,
	`-used 1`,
	`-used +30`,
	`-used -40`,
	`-used 0.5`,
	`-daystart -mtime +1000`,
	`-daystart -mtime -10000`,
	`-mtime +1000 -daystart -mtime +1000`,
	`-newer old`,
	`-newer future`,
	`-newer frac`,
	`-anewer old`,
	`-cnewer old`,
	`-newermm old`,
	`-newerma frac`,
	`-newermc old`,
	`-newerca old`,
	`-newerac frac`,
	`-newercc small`,
	`-newermt '2001-09-09'`,
	`-newermt '2001-09-10'`,
	`-newermt '@1000000000'`,
	`-newermt '@999999999'`,
	`-newerat '2001-09-09 01:46:40'`,
	`-newerct '1 day ago'`,
	`-newermt '2023-11-14 22:13:20.5'`,
	`-newermt 'garbage date'`,
	`-newermt`,
	`-newerBt 2020`,
	`-newermB old`,
	`-newerqq x`,
	`-newerXY x`,
	`-newert x`,
	`-newer nosuch`,
	`-anewer nosuch`,
	`-mtime x`,
	`-mtime ''`,
	`-mtime ' 1'`,
	`-mmin 1x`,
	`-used x`,
	`-used ''`,
}

var findSymlinkLines = []string{
	`-L`,
	`-L -ls`,
	`-L -type l`,
	`-L -xtype l`,
	`-L -type d`,
	`-L -xtype d`,
	`-L -printf '%y%Y %p\n'`,
	`-L -empty`,
	`-L -size 0`,
	`-H . -type l`,
	`-H lsub`,
	`-H lsub/`,
	`-H lsub -maxdepth 1`,
	`-L lsub -maxdepth 1`,
	`lsub -maxdepth 1`,
	`lsub/ -maxdepth 1`,
	`-H dangling`,
	`-L dangling`,
	`-H selfloop`,
	`-L selfloop`,
	`-L sub`,
	`-L sub -prune`,
	`-P -L -P -maxdepth 1 -type l`,
	`-H -L lsub -maxdepth 0`,
	`-L -H lsub -maxdepth 0`,
	`-follow lsub -maxdepth 1`,
	`-L -newer lsmall`,
	`-H -samefile lsmall lsmall small hardlink`,
	`-L -lname '*' -printf '%p %l\n'`,
}

var findExecLines = []string{
	`-name old -exec echo 'X{}Y' ';'`,
	`-name old -exec echo '{}' '{}' ';'`,
	`-name old -exec echo 'a{}b{}c' ';'`,
	`-name '*.c' -exec echo '{}' +`,
	`-name '*.c' -exec echo start '{}' +`,
	`-name '*.c' -execdir echo '{}' +`,
	`-name '*.c' -execdir echo '{}' ';'`,
	`-name '*.c' -execdir pwd ';'`,
	`-name 'est.c' -execdir sh -c 'pwd | sed "s|.*/sub|sub|"' ';'`,
	`-name '*.c' -exec false ';' -print`,
	`-name '*.c' -exec false '{}' + -print`,
	`-name old -exec sh -c 'exit 3' ';' -print`,
	`-name old -exec sh -c 'exit 3' '{}' + -print`,
	`-name old -exec nosuchcmd ';'`,
	`-name old -exec nosuchcmd '{}' +`,
	`-name old -exec sh -c 'kill -9 $$' ';'`,
	`-name old -exec sh -c 'kill -9 $$' '{}' +`,
	`-name old -exec /etc ';'`,
	`-name old -exec ./old ';'`,
	`-name nosuch -exec echo '{}' +`,
	`-name 'k*' -exec sh -c 'echo "$@" | wc -w' sh '{}' + -print`,
	`-maxdepth 1 -name 's*' -print -exec echo '[{}]' ';'`,
	`-name sub -exec echo '{}' ';' -prune`,
	`-name small -exec printf '%s\n' '{}' ';' -fprint /dev/stdout`,
	`-name mid -execdir echo '{}' + -name old`,
	`-name . -execdir echo '{}' ';'`,
	`sub/a.c -execdir echo '{}' ';'`,
	`./sub/ -maxdepth 0 -execdir echo '{}' ';'`,
	`-exec`,
	`-exec ';'`,
	`-exec echo`,
	`-exec echo ';' -exec`,
	`-exec echo '{}' '{}' +`,
	`-exec echo '{}x' +`,
	`-exec echo 'x{}' +`,
	`-exec echo '{}' '{}x' +`,
	`-exec echo '{}{}' +`,
	`-exec echo '{}' x +`,
	`-exec echo + -print`,
	`-execdir ';'`,
	`-ok`,
	`-ok ';'`,
	`-ok echo '{}' +`,
	`-okdir`,
}

var findSyntaxLines = []string{
	`-name`,
	`-foo`,
	`-name x y`,
	`-name x old`,
	`-name x nosuch`,
	`-type d old`,
	`-print old`,
	`-true nosuch`,
	`. -name a b`,
	`'(' -name a`,
	`')' -print`,
	`-a`,
	`!`,
	`-o -print`,
	`-name a -o`,
	`-print -a`,
	`-print -o`,
	`-print ,`,
	`-name a -a`,
	`-name a ,`,
	`-print !`,
	`-name a !`,
	`-print -and`,
	`-print -or`,
	`-name a -or`,
	`-name a -and`,
	`-name a -not`,
	`. -maxdepth 0 -o -print`,
	`. -print -o -daystart`,
	`. -maxdepth 0 -print -o -warn`,
	`. -maxdepth 0 -daystart -o -print`,
	`. -maxdepth 0 ! -daystart`,
	`. -maxdepth 0 ! -follow`,
	`. -maxdepth 0 '(' -daystart ')'`,
	`. -maxdepth 0 -false -o -daystart -print`,
	`'(' ')'`,
	`-maxdepth 0 '(' ')'`,
	`-maxdepth 0 -print ')'`,
	`-maxdepth 0 '(' -print ')' ')'`,
	`-maxdepth 0 -print '(' ')'`,
	`-maxdepth 0 -print ! ')'`,
	`-maxdepth 0 -print -o ')'`,
	`-maxdepth 0 -print , ')'`,
	`-maxdepth 0 '(' -o -print ')'`,
	`-maxdepth 0 '(' ! ')'`,
	`-maxdepth 0 '(' -print -a ')'`,
	`-maxdepth 0 -print '('`,
	`-maxdepth 0 -name a '('`,
	`-maxdepth 0 '(' '(' -print ')'`,
	`-maxdepth 0 -print -o -o -print`,
	`-maxdepth 0 -print -a -o -print`,
	`-maxdepth 0 ! -o -print`,
	`-maxdepth 0 ! ! -print`,
	`-maxdepth 0 , -print`,
	`-maxdepth 0 -print , , -print`,
	`-maxdepth 0 -not -print`,
	`-maxdepth 0 -and -print`,
	`-maxdepth 0 -prune`,
	`-maxdepth 0 -true , -false`,
	`-maxdepth 0 -false -o ! -false -print`,
	`-maxdepth`,
	`-maxdepth -1`,
	`-maxdepth x`,
	`-maxdepth 1x`,
	`-maxdepth +1 -print`,
	`-maxdepth 2147483648`,
	`-maxdepth 2147483647 -name old`,
	`-mindepth 01 -maxdepth 1`,
	`-mindepth`,
	`-HL -maxdepth 0`,
	`-H -maxdepth 0`,
	`-D`,
	`-O -maxdepth 0`,
	`-O0 -maxdepth 0`,
	`-O3 -maxdepth 0`,
	`-Ox -maxdepth 0`,
	`-O1x -maxdepth 0`,
	`-- -maxdepth 0`,
	`-- -- -maxdepth 0`,
	`, -maxdepth 0`,
	`! -maxdepth 0`,
	`-printf`,
	`-fprint`,
	`-fprint0`,
	`-fprintf`,
	`-fprintf out`,
	`-fls`,
	`-fprint /nonexistent/x`,
	`-size`,
	`-size 1T`,
	`-size ''`,
	`-size +`,
	`-size -`,
	`-size 1kk`,
	`-size 99999999999999999999`,
	`-size 3q`,
	`-links x`,
	`-links +-1`,
	`-links 1.5`,
	`-links`,
	`-inum -1`,
	`-inum x`,
	`-uid x`,
	`-uid`,
	`-gid -5`,
	`-user`,
	`-user ''`,
	`-user nosuchuser`,
	`-user 99999`,
	`-group`,
	`-group ''`,
	`-group nosuchgroup`,
	`-type`,
	`-type ''`,
	`-type q`,
	`-type fd`,
	`-type f,`,
	`-type f,,d`,
	`-type ,f`,
	`-type f,f`,
	`-xtype q`,
	`-xtype D`,
	`-perm`,
	`-perm 999`,
	`-perm +444`,
	`-perm +0`,
	`-perm u=xyz`,
	`-perm 07777`,
	`-perm 17777`,
	`-perm ''`,
	`-perm -`,
	`-perm /`,
	`-perm u=r,`,
	`-regextype`,
	`-regextype foo`,
	`-regextype posix-extended -regex '(a'`,
	`-regex '\(a'`,
	`-regex '[a'`,
	`-regex 'a\{1'`,
	`-regextype posix-basic -regex 'a\'`,
	`-regextype posix-extended -regex "a'b("`,
	`-iregex`,
	`-path`,
	`-lname`,
	`-wholename`,
	`-iwholename`,
	`-ipath`,
	`-ilname`,
	`-fstype`,
	`-context`,
	`-context x`,
	`-samefile`,
	`-newer`,
	`-anewer`,
	`-cnewer`,
	`-used`,
	`-newerat`,
	`-delete -prune`,
	`-files0-from`,
	`-files0-from /dev/null`,
	`-files0-from nosuch`,
	`. -files0-from /dev/null`,
	`-warn -L -maxdepth 0`,
	`. -warn -name x -maxdepth 1`,
	`. -warn -name x -depth`,
	`. -warn -name x -mindepth 1`,
	`. -warn -name x -xdev`,
	`. -warn -name x -noleaf`,
	`. -warn -name x -daystart`,
	`. -warn -name x -follow`,
	`. -warn -name x -regextype emacs`,
	`. -warn -name x -ignore_readdir_race`,
	`. -warn -name a/b`,
	`. -warn -name '*/*'`,
	`. -warn -iname a/b`,
	`. -warn -path a/`,
	`. -warn -name '/'`,
	`. -warn -name x -maxdepth 1 -mindepth 0`,
	`. -warn -maxdepth 1 -name x -maxdepth 1`,
	`. -warn -print -maxdepth 0`,
	`. -warn -o -maxdepth 0`,
	`. -warn '(' -maxdepth 0 ')'`,
	`. -warn ! -maxdepth 0`,
	`. -nowarn -name x -maxdepth 1`,
	`. -warn -perm +x -maxdepth 0`,
	`. -follow -warn -maxdepth 0`,
}

var findStartLines = []string{
	`nosuch`,
	`. nosuch`,
	`nosuch sub -maxdepth 0`,
	`small/`,
	`''`,
	`')' -print`,
	`sub -name '*.c'`,
	`. sub -name '*.c'`,
	`sub . -maxdepth 1`,
	`sub/ sub// ./sub -maxdepth 1`,
	`sub/deep/er/est.c`,
	`./sub/ -maxdepth 0 -printf '%f|%h|%P|%p\n'`,
	`sub// -printf '[%h][%f][%d]\n'`,
	`. .// ./// sub/// sub/.// -maxdepth 0 -printf '[%h][%f]\n'`,
	`/ // -maxdepth 0 -printf '[%h][%f][%P][%H]\n'`,
	`/ -maxdepth 0 -name /`,
	`// -maxdepth 0 -name '/'`,
	`sub/ -name sub`,
	`sub// -name sub`,
	`sub/deep/ -name deep`,
	`. -maxdepth 0 -name .`,
	`-name '.'`,
	`locked`,
	`locked -maxdepth 0`,
	`locked/inner`,
	`-name locked -prune -o -print`,
	`-depth -name locked`,
	`-- -name x`,
	`. -- -name x`,
	`-dash`,
	`./-dash`,
}

var findManyLines = []string{
	`many -maxdepth 1 -name 'f0000*'`,
	`many -name 'f1004*'`,
	`many -print -quit`,
	`few`,
	`. -maxdepth 1`,
}

// findInodeCases name files by their inode numbers, which only the shared
// tree can give in advance.
func findInodeCases(dir string) []invocation {
	var cases []invocation
	for _, name := range []string{"small", "sub", "lsmall"} {
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dir, name), &st); err != nil {
			continue
		}
		ino := strconv.FormatUint(uint64(st.Ino), 10)
		cases = append(cases,
			invocation{name: "find -inum " + name, args: []string{"-inum", ino}, dir: dir},
			invocation{name: "find -inum +- " + name, args: []string{"-inum", "-" + ino, "-inum", "+" + strconv.FormatUint(uint64(st.Ino)-2, 10)}, dir: dir},
		)
	}
	return cases
}

// findOkCases answer the prompts of -ok and -okdir on standard input.
func findOkCases(dir string) []invocation {
	return []invocation{
		{name: "find -ok yes", args: []string{"-name", "old", "-ok", "echo", "a", "{}", "b", ";"}, stdin: "y\n", dir: dir},
		{name: "find -ok yes no", args: []string{"-name", "*.c", "-ok", "echo", "{}", ";"}, stdin: "y\nn\n", dir: dir},
		{name: "find -ok Yes", args: []string{"-name", "old", "-ok", "echo", "{}", ";", "-print"}, stdin: "Yes\n", dir: dir},
		{name: "find -ok no answer", args: []string{"-name", "old", "-ok", "echo", "{}", ";", "-print"}, dir: dir},
		{name: "find -ok blank answer", args: []string{"-name", "old", "-ok", "echo", "{}", ";", "-print"}, stdin: "\n", dir: dir},
		{name: "find -okdir", args: []string{"-name", "*.c", "-okdir", "echo", "{}", ";"}, stdin: "y\ny\ny\n", dir: dir},
		{name: "find -ok stdin of the command", args: []string{"-name", "old", "-ok", "cat", ";"}, stdin: "y\nmore\n", dir: dir},
		{name: "find -exec stdin of the command", args: []string{"-name", "old", "-exec", "cat", ";"}, stdin: "rest\n", dir: dir},
		{name: "find -ok newline name", args: []string{"-name", "nl*", "-ok", "echo", "{}", ";"}, stdin: "y\n", dir: dir},
	}
}

// findChangeCases delete or write files, so each side gets its own tree.
func findChangeCases() []invocation {
	lines := []string{
		`-name nosuch -delete`,
		`-delete -prune -depth`,
		`-depth -name nosuch -delete -prune`,
		`-name '*.c' -delete`,
		`full -delete`,
		`-name full -delete`,
		`-delete`,
		`. -maxdepth 1 -name 'k*' -delete -print`,
		`sub -depth -delete`,
		`-name keep -type d -delete`,
		`-name '*x*' -fprint out`,
		`-name '*.c' -fprint0 out0`,
		`-name '*.c' -fprintf outf '%f %d\n'`,
		`-name '*.c' -fprint out -fprint out`,
		`-name '*.c' -fprint out -fprintf out '%f\n'`,
		`-name nosuch -fprint created`,
		`-name '*.c' -fprint out -print`,
		`-name 'k*' -exec rm '{}' ';' -print`,
		`-name 'k*' -exec rm '{}' +`,
		`-type d -name 'emptysub' -execdir rmdir '{}' ';'`,
		`-name small -exec mv '{}' moved ';'`,
		`-name '*.c' -execdir mv '{}' '{}.bak' ';'`,
		`-name '*.c' -execdir sh -c 'mv "$1" "$1.new"' sh '{}' ';'`,
		`-depth -name 'deep' -exec rm -r '{}' ';'`,
		`-name deep -prune -exec rm -r '{}' ';'`,
	}
	var cases []invocation
	for _, line := range lines {
		cases = append(cases, invocation{name: "find " + line, args: shellWords(line), seedTree: findChangeTree})
	}
	return cases
}

// findOutputCases write to an output that fails or to a closed one.
func findOutputCases(dir string) []invocation {
	return []invocation{
		{name: "find to a full stdout", args: []string{"-maxdepth", "1"}, stdout: stdoutFull, dir: dir},
		{name: "find -fprint to a full device", args: []string{"-maxdepth", "0", "-fprint", "/dev/full"}, dir: dir},
		{name: "find -fprint to a full device and stdout", args: []string{"-maxdepth", "0", "-fprint", "/dev/full", "-print"}, dir: dir},
		{name: "find -fprint /dev/stderr", args: []string{"-name", "old", "-fprint", "/dev/stderr"}, dir: dir},
		{name: "find -fprint /dev/stdout", args: []string{"-name", "old", "-fprint", "/dev/stdout", "-print"}, dir: dir},
		{name: "find -fls /dev/stdout", args: []string{"-name", "old", "-fls", "/dev/stdout"}, dir: dir},
		{name: "find errors and output merged", args: []string{"nosuch", ".", "-maxdepth", "1", "-name", "s*"}, merged: true, dir: dir},
		{name: "find -exec output order", args: []string{"-maxdepth", "1", "-name", "s*", "-print", "-exec", "echo", "+{}", ";"}, merged: true, dir: dir},
		{name: "find warn on a terminal", args: []string{".", "-name", "x", "-maxdepth", "1"}, ttyIn: true, dir: dir},
		{name: "find nowarn on a terminal", args: []string{".", "-nowarn", "-name", "x", "-maxdepth", "1"}, ttyIn: true, dir: dir},
		{name: "find to a closed stdout", args: []string{"-maxdepth", "0"}, stdout: stdoutClosed, dir: dir},
		{name: "find -fls to a file", args: []string{dir, "-name", "k*", "-fls", "outl"}, artifacts: []string{"outl"}},
		{name: "find -fprintf to a file", args: []string{dir, "-name", "*.c", "-fprintf", "outp", "%i %p\n", "-fprint0", "out0"}, artifacts: []string{"outp", "out0"}},
	}
}

func TestFindParity(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("find needs a POSIX file system")
	}
	requireParity(t, "find", findCases(t))
}

func TestFindHelpVersion(t *testing.T) {
	requireHelp(t, "find", []string{"--help"}, 0)
	requireHelp(t, "find", []string{"-help"}, 0)
	requireVersion(t, "find", []string{"--version"}, 0)
	requireVersion(t, "find", []string{"-version"}, 0)
}

// -D writes GNU find's own trace of its work. The port does not have it,
// and says so instead of running without the trace.
func TestFindDebugIsRefused(t *testing.T) {
	cmd := exec.Command(fernBin(t, "find"), "-D", "stat", ".")
	cmd.Dir = t.TempDir()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("find -D stat: want exit 1, got %v", err)
	}
	if len(out) != 0 || !strings.HasSuffix(stderr.String(), ": this port does not have the option '-D'\n") {
		t.Fatalf("find -D stat: stdout %q, stderr %q", out, stderr.String())
	}
}
