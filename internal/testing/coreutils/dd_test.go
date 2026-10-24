package coreutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func init() {
	registerCorpus("dd", ddCases)
}

func ddSeed(t *testing.T, dir string) {
	t.Helper()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("in10", "abcdefghij")
	write("in5000", strings.Repeat("Z", 5000))
	write("pre", "0123456789")
	write("lines", "a\nbcdef\nghi\n")
	write("recs", "ab  cd  ef  ")
	write("lines2", "a\n\nlonger than ten bytes\nxy  \nend")
	write("recs2", "ab      a b abcdx")
	write("mixed", "caf\xc3\xa9 A\xffz Z\n")
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	write("all256", string(all))
	write("erecs", "\x81\x82\x40\x40\x83\x84\x40\x40")
	write("in5", "abcde")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("in10", filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
}

func ddSparseSeed(t *testing.T, dir string) {
	t.Helper()
	ddSeed(t, dir)
	nul := func(n int) string { return strings.Repeat("\x00", n) }
	for name, content := range map[string]string{
		"zmid":  nul(8192) + strings.Repeat("x", 100) + nul(8192),
		"zend":  strings.Repeat("x", 10) + nul(20000),
		"zall":  nul(20000),
		"pre20": strings.Repeat("P", 20000),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func ddCases(t *testing.T) []invocation {
	var cases []invocation
	add := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: ddSeed})
	}
	addSparse := func(name string, args ...string) {
		cases = append(cases, invocation{name: name, args: args, seedTree: ddSparseSeed, sparse: true})
	}

	add("plain", "if=in10", "of=out", "status=noxfer")
	add("bs-4", "if=in10", "of=out", "bs=4", "status=noxfer")
	add("bs-4096-short-last", "if=in5000", "of=out", "bs=4096", "status=noxfer")
	add("ibs-obs-differ", "if=in10", "of=out", "ibs=3", "obs=4", "status=noxfer")
	add("ibs-obs-differ-quiet", "if=in10", "of=out", "ibs=3", "obs=4", "status=none")
	add("count-blocks", "if=in5000", "of=out", "bs=1024", "count=2", "status=noxfer")
	add("count-zero", "if=in10", "of=out", "count=0", "status=noxfer")
	add("count-bytes", "if=in5000", "of=out", "bs=1024", "count=100", "iflag=count_bytes", "status=noxfer")
	add("count-multiplied", "if=in10", "of=out", "bs=1", "count=2x3", "status=noxfer")
	add("bs-multiplied", "if=in10", "of=out", "bs=1kx2", "count=1", "status=noxfer")
	add("bs-overrides-later-obs", "if=in10", "of=out", "bs=3", "obs=7", "status=noxfer")
	add("bs-overrides-later-ibs", "if=lines", "of=out", "bs=3", "ibs=5", "status=noxfer")

	add("skip-and-seek", "if=in10", "of=out", "bs=1", "skip=3", "seek=2", "status=noxfer")
	add("skip-bytes", "if=in10", "of=out", "bs=4", "skip=3", "iflag=skip_bytes", "status=noxfer")
	add("seek-bytes", "if=in10", "of=out", "bs=4", "seek=3", "oflag=seek_bytes", "status=noxfer")
	add("skip-to-eof", "if=in10", "of=out", "bs=1", "skip=10", "status=noxfer")
	add("skip-past-eof", "if=in10", "of=out", "bs=1", "skip=11", "status=noxfer")
	add("skip-far-past-eof", "if=in10", "of=out", "bs=1", "skip=50", "status=noxfer")
	add("seek-truncates", "if=in10", "of=pre", "bs=1", "seek=2", "count=3", "status=noxfer")
	add("seek-notrunc", "if=in10", "of=pre", "bs=1", "seek=2", "count=3", "conv=notrunc", "status=noxfer")
	add("notrunc-shorter", "if=in10", "of=pre", "bs=1", "count=3", "conv=notrunc", "status=noxfer")
	add("append", "if=in10", "of=pre", "oflag=append", "conv=notrunc", "status=noxfer")

	add("iflag-directory-regular", "if=in10", "of=out", "iflag=directory", "status=noxfer")
	add("iflag-directory-dir", "if=d", "of=out", "iflag=directory", "status=noxfer")
	add("iflag-nofollow-symlink", "if=lnk", "of=out", "iflag=nofollow", "status=noxfer")
	add("iflag-nofollow-regular", "if=in10", "of=out", "iflag=nofollow", "status=noxfer")
	add("iflag-sync-flags", "if=in10", "of=out", "iflag=dsync,sync,noctty", "status=noxfer")
	add("oflag-sync-flags", "if=in10", "of=out", "oflag=dsync,sync,noctty,nofollow", "status=noxfer")
	add("oflag-directory-regular", "if=in10", "of=pre", "oflag=directory", "conv=notrunc", "status=noxfer")
	add("oflag-nofollow-symlink", "if=in10", "of=lnk", "oflag=nofollow", "conv=notrunc", "status=noxfer")
	add("oflag-fullblock-invalid", "if=in10", "oflag=fullblock")

	add("iseek", "if=in10", "of=out", "bs=1", "iseek=3", "status=noxfer")
	add("oseek", "if=in10", "of=out", "bs=2", "oseek=1", "status=noxfer")
	add("skip-then-iseek", "if=in10", "of=out", "bs=1", "skip=1", "iseek=3", "status=noxfer")
	add("iseek-then-skip", "if=in10", "of=out", "bs=1", "iseek=3", "skip=1", "status=noxfer")
	add("oseek-notrunc", "if=in10", "of=pre", "bs=1", "oseek=2", "count=3", "conv=notrunc", "status=noxfer")
	add("iseek-invalid-number", "if=in10", "iseek=q")

	add("suffix-c", "if=in10", "of=out", "bs=1", "count=1cx7", "status=noxfer")
	add("suffix-c-bare", "if=in10", "of=out", "bs=1", "count=c", "status=noxfer")
	add("suffix-w", "if=in10", "of=out", "bs=1", "count=3w", "status=noxfer")
	add("suffix-w-bare", "if=in10", "of=out", "bs=1", "count=w", "status=noxfer")
	add("suffix-b", "if=in10", "of=out", "bs=1", "count=b", "status=noxfer")
	add("suffix-c-on-bs", "if=in10", "of=out", "bs=3c", "status=noxfer")
	add("suffix-w-on-bs", "if=in10", "of=out", "bs=2w", "status=noxfer")
	add("count-bytes-suffix", "if=in5000", "of=out", "bs=100", "count=2B", "status=noxfer")
	add("count-bytes-suffix-kb", "if=in5000", "of=out", "bs=100", "count=1kB", "status=noxfer")
	add("count-bytes-suffix-kib", "if=in5000", "of=out", "bs=100", "count=1KiB", "status=noxfer")
	add("count-blocks-no-suffix", "if=in5000", "of=out", "bs=100", "count=1k", "status=noxfer")
	add("count-bytes-suffix-product", "if=in5000", "of=out", "bs=100", "count=1cx7Bx2", "status=noxfer")
	add("count-bytes-suffix-after-b", "if=in5000", "of=out", "bs=100", "count=1bB", "status=noxfer")
	add("skip-bytes-suffix", "if=in10", "of=out", "bs=4", "skip=3B", "status=noxfer")
	add("seek-bytes-suffix", "if=in10", "of=out", "bs=4", "seek=3B", "status=noxfer")
	add("bytes-suffix-on-bs", "if=in10", "of=out", "bs=3B", "status=noxfer")
	add("invalid-number-uppercase-c", "if=in10", "count=1C")
	add("invalid-number-uppercase-w", "if=in10", "count=1W")
	add("invalid-number-c-then-ib", "if=in10", "count=1ciB")
	add("invalid-number-c-then-digit", "if=in10", "count=1c0")
	add("invalid-number-k-then-c", "if=in10", "count=1kc")
	add("invalid-number-lowercase-b-after-k", "if=in10", "count=1kb")
	add("invalid-number-double-b", "if=in10", "count=1BB")
	add("invalid-number-bare-b", "if=in10", "count=B")
	add("number-overflow", "if=in10", "count=9223372036854775808")
	add("number-overflow-unsigned", "if=in10", "count=18446744073709551616")
	add("number-overflow-product", "if=in10", "count=9223372036854775807x9223372036854775807")
	add("number-at-intmax", "if=in10", "of=out", "count=9223372036854775807", "status=noxfer")
	add("zero-multiplier", "if=in10", "of=out", "count=0x3", "status=noxfer")
	add("zero-multiplier-twice", "if=in10", "of=out", "count=0x0x3", "status=noxfer")
	add("zero-multiplier-spelled-out", "if=in10", "of=out", "count=00x3", "status=noxfer")
	add("zero-multiplier-on-bs", "if=in10", "bs=0x3")
	add("zero-multiplier-trailing", "if=in10", "of=out", "count=3x0", "status=noxfer")

	add("conv-ucase", "if=in10", "of=out", "conv=ucase", "status=noxfer")
	add("conv-lcase", "if=in10", "of=out", "conv=lcase", "bs=4", "status=noxfer")
	add("conv-swab", "if=in10", "of=out", "conv=swab", "status=noxfer")
	add("conv-swab-odd", "if=in10", "of=out", "bs=3", "conv=swab", "status=noxfer")
	add("conv-sync-pads", "if=in10", "of=out", "ibs=4", "conv=sync", "status=noxfer")
	add("conv-two", "if=in10", "of=out", "bs=4", "conv=ucase,swab", "status=noxfer")
	add("conv-swab-words", "if=all256", "of=out", "conv=swab", "status=noxfer")
	add("conv-swab-words-bs5", "if=all256", "of=out", "bs=5", "conv=swab", "status=noxfer")
	add("conv-swab-words-bs9", "if=all256", "of=out", "bs=9", "conv=swab", "status=noxfer")
	add("conv-swab-words-bs17", "if=all256", "of=out", "ibs=17", "obs=8", "conv=swab", "status=noxfer")
	add("conv-ucase-non-ascii", "if=mixed", "of=out", "conv=ucase", "status=noxfer")
	add("conv-lcase-non-ascii", "if=mixed", "of=out", "conv=lcase", "status=noxfer")
	add("conv-ucase-small-blocks", "if=mixed", "of=out", "bs=3", "conv=ucase", "status=noxfer")
	add("conv-lcase-and-swab", "if=mixed", "of=out", "bs=3", "conv=lcase,swab", "status=noxfer")

	add("conv-block", "if=lines", "of=out", "conv=block", "cbs=4", "status=noxfer")
	add("conv-block-truncates", "if=lines", "of=out", "conv=block", "cbs=2", "status=noxfer")
	add("conv-block-no-cbs", "if=lines", "of=out", "conv=block", "status=noxfer")
	add("conv-unblock", "if=recs", "of=out", "conv=unblock", "cbs=4", "status=noxfer")
	add("conv-unblock-partial", "if=recs", "of=out", "conv=unblock", "cbs=5", "status=noxfer")
	add("conv-block-split-reads", "if=lines2", "of=out", "bs=3", "conv=block", "cbs=10", "status=noxfer")
	add("conv-block-split-truncates", "if=lines2", "of=out", "bs=4", "conv=block", "cbs=3", "status=noxfer")
	add("conv-block-one-byte-reads", "if=lines2", "of=out", "bs=1", "conv=block", "cbs=6", "status=noxfer")
	add("conv-block-whole", "if=lines2", "of=out", "conv=block", "cbs=10", "status=noxfer")
	add("conv-unblock-split-reads", "if=recs2", "of=out", "bs=3", "conv=unblock", "cbs=4", "status=noxfer")
	add("conv-unblock-one-byte-reads", "if=recs2", "of=out", "bs=1", "conv=unblock", "cbs=4", "status=noxfer")
	add("conv-unblock-whole", "if=recs2", "of=out", "conv=unblock", "cbs=4", "status=noxfer")
	add("conv-block-and-unblock", "if=lines", "of=out", "conv=block,unblock", "cbs=4", "status=noxfer")
	add("conv-block-and-unblock-no-cbs", "if=lines", "of=out", "conv=block,unblock", "status=noxfer")
	add("conv-block-sync-no-cbs", "if=in10", "of=out", "ibs=4", "conv=block,sync", "status=noxfer")
	add("conv-swab-unblock-held-byte", "if=in5", "of=out", "conv=swab,unblock", "cbs=3", "status=noxfer")
	add("conv-swab-sync-order", "if=in5", "of=out", "ibs=4", "conv=swab,sync", "status=noxfer")

	add("conv-ascii-all-bytes", "if=all256", "of=out", "conv=ascii", "status=noxfer")
	add("conv-ebcdic-all-bytes", "if=all256", "of=out", "conv=ebcdic", "status=noxfer")
	add("conv-ibm-all-bytes", "if=all256", "of=out", "conv=ibm", "status=noxfer")
	add("conv-ascii-unblocks", "if=erecs", "of=out", "conv=ascii", "cbs=4", "status=noxfer")
	add("conv-ebcdic-blocks", "if=lines", "of=out", "conv=ebcdic", "cbs=4", "status=noxfer")
	add("conv-ibm-blocks-truncates", "if=lines", "of=out", "conv=ibm", "cbs=2", "status=noxfer")
	add("conv-ucase-ascii", "if=all256", "of=out", "conv=ucase,ascii", "status=noxfer")
	add("conv-ebcdic-lcase", "if=all256", "of=out", "conv=ebcdic,lcase", "status=noxfer")
	add("conv-ibm-ucase-swab", "if=mixed", "of=out", "bs=3", "conv=ibm,ucase,swab", "status=noxfer")
	add("conv-ebcdic-sync-pads-ebcdic-space", "if=in10", "of=out", "ibs=4", "conv=ebcdic,sync", "cbs=20", "status=noxfer")
	add("conv-ebcdic-sync-no-cbs", "if=in10", "of=out", "ibs=4", "conv=ebcdic,sync", "status=noxfer")
	add("conv-ascii-with-block-no-cbs", "if=in10", "of=out", "conv=ascii,block", "status=noxfer")

	addSparse("conv-sparse-middle", "if=zmid", "of=out", "bs=4096", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-tail", "if=zend", "of=out", "bs=4096", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-all-nul", "if=zall", "of=out", "bs=4096", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-assembled", "if=zmid", "of=out", "ibs=1000", "obs=4096", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-seek", "if=zall", "of=out", "bs=4096", "seek=2", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-notrunc-keeps-bytes", "if=zmid", "of=pre20", "bs=4096", "conv=sparse,notrunc", "status=noxfer")
	addSparse("conv-sparse-truncates", "if=zmid", "of=pre20", "bs=4096", "conv=sparse", "status=noxfer")
	addSparse("conv-sparse-append", "if=zmid", "of=pre20", "bs=4096", "oflag=append", "conv=sparse,notrunc", "status=noxfer")
	addSparse("conv-sparse-append-all-nul", "if=zall", "of=pre20", "bs=4096", "oflag=append", "conv=sparse,notrunc", "status=noxfer")
	addSparse("conv-sparse-pipe", "if=zmid", "bs=4096", "conv=sparse", "status=noxfer")

	add("conv-ascii-and-ebcdic", "if=in10", "conv=ascii,ebcdic")
	add("conv-ebcdic-and-ibm", "if=in10", "conv=ebcdic,ibm")
	add("conv-ascii-and-block", "if=in10", "conv=ascii,block", "cbs=4")
	add("conv-lcase-and-ucase", "if=in10", "conv=lcase,ucase")
	add("conv-excl-and-nocreat", "if=in10", "conv=excl,nocreat")
	add("conv-records-before-case", "if=in10", "conv=lcase,ucase,block,unblock", "cbs=4")
	add("conv-charsets-first", "if=in10", "conv=ascii,ebcdic,lcase,ucase,block", "cbs=4")

	add("no-such-input", "if=nosuch", "of=out", "status=noxfer")
	add("input-is-a-directory", "if=d", "of=out", "status=noxfer")
	add("output-is-a-directory", "if=in10", "of=d", "status=noxfer")
	add("conv-excl-exists", "if=in10", "of=pre", "conv=excl", "status=noxfer")
	add("conv-excl-fresh", "if=in10", "of=fresh", "conv=excl", "status=noxfer")
	add("conv-nocreat-missing", "if=in10", "of=nosuch2", "conv=nocreat", "status=noxfer")
	add("conv-nocreat-exists", "if=in10", "of=pre", "conv=nocreat", "status=noxfer")
	add("conv-fsync", "if=in10", "of=out", "conv=fsync", "status=noxfer")
	add("conv-fdatasync", "if=in10", "of=out", "conv=fdatasync", "status=noxfer")
	add("conv-fdatasync-no-data-sync", "if=in10", "of=/dev/null", "conv=fdatasync", "status=noxfer")
	add("conv-fsync-no-sync", "if=in10", "of=/dev/null", "conv=fsync", "status=noxfer")
	add("conv-both-syncs-no-sync", "if=in10", "of=/dev/null", "conv=fdatasync,fsync", "status=noxfer")

	add("unrecognized-operand", "bogus=1")
	add("operand-without-equals", "in10")
	add("bare-dash", "-")
	add("unrecognized-option", "--bogus")
	add("invalid-number", "if=in10", "bs=nope")
	add("invalid-number-empty", "if=in10", "bs=")
	add("invalid-number-negative", "if=in10", "count=-1")
	add("invalid-number-zero-bs", "if=in10", "bs=0")
	add("invalid-number-trailing-x", "if=in10", "bs=1x")
	add("invalid-conversion", "if=in10", "conv=bogus")
	add("invalid-conversion-empty", "if=in10", "conv=")
	add("invalid-input-flag", "if=in10", "iflag=bogus")
	add("invalid-output-flag", "if=in10", "oflag=bogus")
	add("invalid-status", "if=in10", "status=bogus")
	add("nocache-input", "if=in5000", "of=out", "bs=1024", "iflag=nocache", "status=noxfer")
	add("nocache-output", "if=in5000", "of=out", "bs=1024", "oflag=nocache", "status=noxfer")
	add("nocache-both-count0", "if=in10", "of=out", "iflag=nocache", "oflag=nocache", "count=0", "status=noxfer")
	add("nocache-stdout-count0", "if=in10", "oflag=nocache", "count=0", "status=noxfer")
	add("last-name-wins", "if=pre", "if=in10", "of=out", "status=noxfer")
	add("dash-is-a-name", "if=-", "of=out", "status=noxfer")

	cases = append(cases,
		invocation{name: "stdin-to-stdout", args: []string{"status=noxfer"}, stdin: "hello dd", seedTree: ddSeed},
		invocation{name: "stdin-skip", args: []string{"bs=1", "skip=3", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-skip-past-eof", args: []string{"bs=1", "skip=11", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-fullblock", args: []string{"bs=4", "iflag=fullblock", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-to-file", args: []string{"of=out", "bs=3", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-nocache", args: []string{"of=out", "iflag=nocache", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
		invocation{name: "stdin-nocache-count0", args: []string{"of=out", "iflag=nocache", "count=0", "status=noxfer"}, stdin: "abcdefghij", seedTree: ddSeed},
	)

	return cases
}

func TestDD(t *testing.T) {
	requireParity(t, "dd", ddCases(t))
}

func TestDDProgress(t *testing.T) {
	duration := regexp.MustCompile(`copied, [0-9]+[.e][0-9e.+-]* s,`)
	run := func(bin string, sleep time.Duration) string {
		t.Helper()
		dir := t.TempDir()
		fifo := filepath.Join(dir, "slow")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		fed := make(chan struct{})
		go func() {
			defer close(fed)
			f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			defer f.Close()
			f.Write([]byte("a"))
			time.Sleep(sleep)
			f.Write([]byte("b"))
		}()
		inv := invocation{args: []string{"if=slow", "of=out", "bs=1", "status=progress"}, dir: dir}
		got := inv.run(t, bin, "dd")
		<-fed
		if got.exit != 0 {
			t.Fatalf("%s: exit = %d, want 0\nstderr:\n%s", bin, got.exit, got.stderr)
		}
		return duration.ReplaceAllString(string(got.stderr), "copied, T s,")
	}
	for _, c := range []struct {
		name  string
		sleep time.Duration
		lines int
	}{
		{"no tick", 100 * time.Millisecond, 0},
		{"one second rounds down", 1300 * time.Millisecond, 1},
		{"one second rounds up", 1700 * time.Millisecond, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := run(referenceBin(t, "dd"), c.sleep)
			got := run(fernBin(t, "dd"), c.sleep)
			if got != want {
				t.Errorf("stderr differs with the durations masked\n gnu: %q\nfern: %q", want, got)
			}
			if n := strings.Count(got, "\r"); n != c.lines {
				t.Errorf("%d progress lines, want %d: %q", n, c.lines, got)
			}
		})
	}
}

func TestDDInfoSignal(t *testing.T) {
	info := syscall.SIGUSR1
	if runtime.GOOS == "darwin" {
		info = syscall.Signal(29)
	}
	duration := regexp.MustCompile(`copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]?B/s`)
	run := func(bin string, env []string, args ...string) (string, *os.ProcessState) {
		t.Helper()
		dir := t.TempDir()
		fifo := filepath.Join(dir, "slow")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		wrote := make(chan struct{})
		release := make(chan struct{})
		go func() {
			f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				close(wrote)
				return
			}
			f.Write([]byte("abc"))
			close(wrote)
			<-release
			f.Close()
		}()
		argv := crossArgv(bin, append([]string{"if=slow", "of=out"}, args...)...)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Env = append(baseEnv(), env...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		<-wrote
		time.Sleep(500 * time.Millisecond)
		if err := cmd.Process.Signal(info); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond)
		close(release)
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Fatalf("%s did not finish after the FIFO closed\nstderr: %q", bin, stderr.String())
		}
		return duration.ReplaceAllString(stderr.String(), "copied, T s, R/s"), cmd.ProcessState
	}
	for _, c := range []struct {
		name    string
		env     []string
		args    []string
		reports int
	}{
		{"noxfer", nil, []string{"status=noxfer"}, 2},
		{"default status", nil, nil, 2},
		{"none", nil, []string{"status=none"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			want, wst := run(referenceBin(t, "dd"), c.env, c.args...)
			got, gst := run(fernBin(t, "dd"), c.env, c.args...)
			if gst.ExitCode() != wst.ExitCode() {
				t.Errorf("exit = %d, gnu %d\nstderr: %q", gst.ExitCode(), wst.ExitCode(), got)
			}
			if got != want {
				t.Errorf("stderr differs with the durations masked\n gnu: %q\nfern: %q", want, got)
			}
			if n := strings.Count(got, "records in\n"); n != c.reports {
				t.Errorf("%d reports, want %d: %q", n, c.reports, got)
			}
		})
	}
	t.Run("POSIXLY_CORRECT", func(t *testing.T) {
		if runtime.GOOS == "darwin" {
			t.Skip("Darwin has SIGINFO, which POSIXLY_CORRECT does not touch")
		}
		for _, bin := range []string{referenceBin(t, "dd"), fernBin(t, "dd")} {
			_, st := run(bin, []string{"POSIXLY_CORRECT=1"}, "status=noxfer")
			ws, ok := st.Sys().(syscall.WaitStatus)
			if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGUSR1 {
				t.Errorf("%s: %v, want death by SIGUSR1", bin, st)
			}
		}
	})
}

func TestDDHelp(t *testing.T) {
	requireHelp(t, "dd", []string{"--help"}, 0)
}

func TestDDVersion(t *testing.T) {
	requireVersion(t, "dd", []string{"--version"}, 0)
}

var ddTransferSmall = regexp.MustCompile(`^10 bytes copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]B/s\n$`)

var ddTransferLarge = regexp.MustCompile(`^2048 bytes \(2\.0 kB, 2\.0 KiB\) copied, [0-9.e+-]+ s, [0-9.]+ [kMGTPE]B/s\n$`)

func TestDDTransferLine(t *testing.T) {
	dir := t.TempDir()
	ddSeed(t, dir)
	for _, c := range []struct {
		name string
		args []string
		want *regexp.Regexp
	}{
		{"ten bytes", []string{"if=in10", "of=out"}, ddTransferSmall},
		{"two kibibytes", []string{"if=in5000", "of=out", "bs=1024", "count=2"}, ddTransferLarge},
	} {
		t.Run(c.name, func(t *testing.T) {
			inv := invocation{args: c.args, dir: dir}
			got := inv.run(t, fernBin(t, "dd"), "dd")
			if got.exit != 0 {
				t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
			}
			lines := strings.SplitAfter(string(got.stderr), "\n")
			if len(lines) < 3 {
				t.Fatalf("report has %d lines, want 3:\n%s", len(lines), got.stderr)
			}
			if got := lines[2]; !c.want.MatchString(got) {
				t.Errorf("transfer line = %q, want match %s", got, c.want)
			}
		})
	}
}
