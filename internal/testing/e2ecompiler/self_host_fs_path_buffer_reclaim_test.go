package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Self-host RC: an fs runtime leaf hands back every raw block it allocates for
// one call — the NUL-terminated path buffer above all, on the SUCCESS path as
// well as the error one (#8813).
//
// Each leaf in asmcore's fs bundle copies its `path` argument into a
// __raw_alloc(plen + 1) block so openat / fstatat / unlinkat get a C string. The
// error path boxed that block into the IoError it returns, so it had an owner;
// the success path named it nowhere and the block was simply lost — one per
// call, `24 + plen + 1` bytes rounded to a freelist class, so the leak grew with
// the PATH. That is what these legs read: the same program at two path lengths
// must cost the same live bytes, which a per-call constant satisfies and a
// per-call path copy cannot. Round-count independence is checked on every leg
// that reaches zero — 20 rounds and 200 must cost the same — so a fix that
// merely shrank the stranded block would still fail.

// fsPathPad is the run of `./` components that stretches a path without changing
// what it resolves to: 24 of them add 48 characters, the gap between the 9- and
// 57-character paths #8813 measured. One file under two spellings is what keeps
// the two measurements otherwise identical.
const fsPathPad = 24

func fsShortPath(dir, base string) string { return filepath.Join(dir, base) }

func fsLongPath(dir, base string) string {
	return dir + "/" + strings.Repeat("./", fsPathPad) + base
}

// fsBase is a fixed name under the work tree, for a leg whose block sizes do not
// depend on how long the path is.
func fsBase(name string) func(work string) string {
	return func(string) string { return name }
}

func fsLoop(rounds int, body string) string {
	return fmt.Sprintf(`function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < %d) {
%s
        i = i + 1;
    }
    if (acc < 0) { return 7; }
    return 0;
}`, rounds, body)
}

func fsOpenCloseSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(`        match (open_reader("%s")) {
            Ok(r) => { match (r.close()) { Some(e) => { return 9; }, None => {} } },
            Err(e) => { return 8; }
        }`, p))
}

func fsReadFileSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (read_file("%s")) { Ok(s) => { acc = acc + s.len(); }, Err(e) => { return 8; } }`, p))
}

func fsReadFileBytesSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (read_file_bytes("%s")) { Ok(b) => { acc = acc + b.len(); }, Err(e) => { return 8; } }`, p))
}

func fsWriteFileSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (write_file("%s", "payload")) { Ok(_) => {}, Err(e) => { return 8; } }`, p))
}

func fsAccessSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (access("%s", 0)) { Ok(_) => {}, Err(e) => { return 8; } }`, p))
}

// fsWriteRemoveSrc covers remove_file, whose path buffer outlived its unlinkat.
func fsWriteRemoveSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(`        match (write_file("%[1]s", "payload")) { Ok(_) => {}, Err(e) => { return 8; } }
        match (remove_file("%[1]s")) { Ok(_) => {}, Err(e) => { return 9; } }`, p))
}

// fsDirTreeSrc covers create_dir_all and remove_dir_all — the two leaves whose
// path buffer is read AFTER the first syscall (create_dir_all rewrites it
// component by component, remove_dir_all still needs it for the final rmdir),
// so neither could be handed back where the others are. remove_dir_all also
// allocates a 64 KiB dirent buffer per getdents round and an 8-byte cursor, both
// of which it used to abandon, and one block per child it recurses into —
// see fsDirTreeBase for the length that block is measured at.
func fsDirTreeSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(`        match (create_dir_all("%[1]s/x/y")) { Ok(_) => {}, Err(e) => { return 8; } }
        match (remove_dir_all("%[1]s/x")) { Ok(_) => {}, Err(e) => { return 9; } }`, p))
}

// fsStatSrc and fsReadDirSrc return an Ok payload beside the path buffer:
// stat's FileStat struct and read_dir's string[] of names. Both are reclaimed.
func fsStatSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (stat("%s")) { Ok(st) => { acc = acc + 1; }, Err(e) => { return 8; } }`, p))
}

func fsReadDirSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (read_dir("%s")) { Ok(ns) => { acc = acc + ns.len(); }, Err(e) => { return 8; } }`, p))
}

// fsWork lays out the tree the probes run against: one readable file, a scratch
// name the write/remove leg owns, a directory holding exactly one entry (so both
// spellings of the read_dir leg see the same number of names), and a file whose
// bytes are not UTF-8.
// fsDirTreeBase names the tree the dir_tree leg owns, padded so the child path
// remove_dir_all builds — `plen + 1 + namelen` bytes for "<tree>/x" plus "y" —
// is a MULTIPLE OF 8. That is the length at which a block allocated one byte
// longer than the string boxed over it lands in a freelist class its extent
// does not cover: __fern_str_free's fused arm sizes from the LENGTH, so the
// mismatch is a silent 8 bytes a round at these lengths and nothing at others.
// The long spelling adds 48 characters, a multiple of 8, so one padding serves
// both. This is the trap #8402 recorded, and the reason the child path carries
// no NUL of its own: the recursive call takes a Fern string and terminates its
// own copy.
func fsDirTreeBase(work string) string {
	// cl = len(work) + len("/") + len(base) + len("/x") + 1 + len("y")
	pad := (8 - (len(work)+len("/")+len("tree")+len("/x")+1+len("y"))%8) % 8
	return "tree" + strings.Repeat("z", pad)
}

func fsWork(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	put := func(p string, content []byte) {
		t.Helper()
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(work, "f"), []byte("payload"))
	if err := os.MkdirAll(filepath.Join(work, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(work, "d", "x"), []byte("payload"))
	put(filepath.Join(work, "bad"), []byte{0xff, 0xfe, 0xfd, 'x'})
	return work
}

func TestSelfHostFsPathBufferReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	work := fsWork(t)

	const (
		few  = 20
		many = 200
	)

	for _, tc := range []struct {
		name string
		base func(work string) string
		src  func(rounds int, path string) string
	}{
		{name: "open_close", base: fsBase("f"), src: fsOpenCloseSrc},
		{name: "read_file", base: fsBase("f"), src: fsReadFileSrc},
		{name: "read_file_bytes", base: fsBase("f"), src: fsReadFileBytesSrc},
		{name: "write_file", base: fsBase("f"), src: fsWriteFileSrc},
		{name: "access", base: fsBase("f"), src: fsAccessSrc},
		{name: "write_remove", base: fsBase("scratch"), src: fsWriteRemoveSrc},
		{name: "dir_tree", base: fsDirTreeBase, src: fsDirTreeSrc},
		{name: "stat", base: fsBase("f"), src: fsStatSrc},
		{name: "read_dir", base: fsBase("d"), src: fsReadDirSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := fsMeasure(t, gcc, runner, dir, driverBin, tc.name)
			base := tc.base(work)
			short, long := fsShortPath(work, base), fsLongPath(work, base)
			if len(long)-len(short) < 40 {
				t.Fatalf("the two spellings differ by %d characters, too few to separate a path-sized leak",
					len(long)-len(short))
			}

			sa, sf, sl := measure("short", tc.src(many, short))
			la, lf, ll := measure("long", tc.src(many, long))
			if sl != ll {
				t.Errorf("live_bytes %d at a %d-character path vs %d at a %d-character one "+
					"(allocs %d/%d, frees %d/%d): the NUL-terminated path buffer is left behind every call",
					sl, len(short), ll, len(long), sa, la, sf, lf)
			}

			fa, ff, fl := measure("few", tc.src(few, short))
			if fl != sl {
				t.Errorf("live_bytes %d at rounds=%d vs %d at rounds=%d (allocs %d/%d, frees %d/%d): "+
					"something is left behind every round", fl, few, sl, many, fa, sa, ff, sf)
			}
			if sa != sf || sl != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — every block the loop allocates must be freed",
					sa, sf, sl)
			}
		})
	}
}

// TestSelfHostFsErrorPathBufferReclaimX86_64 — the failing calls. read_file
// allocates its content buffer from the file's size and must not abandon it when
// the read or the UTF-8 check fails, which on a directory is a 4 KiB block a
// call. Every block a failing call allocates, the IoError and its path string
// included, is reclaimed.
func TestSelfHostFsErrorPathBufferReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	work := fsWork(t)

	const (
		few  = 20
		many = 200
	)
	fail := func(call string) func(rounds int, path string) string {
		return func(rounds int, path string) string {
			return fsLoop(rounds, fmt.Sprintf(
				`        match (%s) { Ok(_) => { return 8; }, Err(e) => {} }`,
				fmt.Sprintf(call, path)))
		}
	}

	for _, tc := range []struct {
		name string
		base string
		src  func(rounds int, path string) string
	}{
		{name: "open_missing", base: "nope", src: fail(`open_reader("%s")`)},
		{name: "read_file_missing", base: "nope", src: fail(`read_file("%s")`)},
		// A directory opens and stats, so the failure lands after the content
		// buffer is allocated — the block this leg exists for.
		{name: "read_file_eisdir", base: "d", src: fail(`read_file("%s")`)},
		{name: "read_file_bad_utf8", base: "bad", src: fail(`read_file("%s")`)},
		{name: "stat_missing", base: "nope", src: fail(`stat("%s")`)},
		{name: "read_dir_missing", base: "nope", src: fail(`read_dir("%s")`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := fsMeasure(t, gcc, runner, dir, driverBin, tc.name)
			p := fsShortPath(work, tc.base)
			for _, rounds := range []int{few, many} {
				a, f, l := measure(fmt.Sprintf("r%d", rounds), tc.src(rounds, p))
				if a != f || l != 0 {
					t.Errorf("allocs=%d frees=%d live_bytes=%d over %d rounds — every block a "+
						"failing call allocates must be freed", a, f, l, rounds)
				}
			}
		})
	}
}

// fsMeasure compiles one program through the self-host driver under
// FERN_LEAKCHECK and returns its allocs / frees / live_bytes.
func fsMeasure(t *testing.T, gcc string, runner []string, dir, driverBin, name string) func(label, src string) (int64, int64, int64) {
	return func(label, src string) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		bin := buildBin(t, gcc, dir, name+"_"+label, asm)
		stderr, exit := runWithStdin(t, runner, bin, nil)
		if exit != 0 {
			t.Fatalf("%s/%s exited %d (8/9 = the call answered the wrong way; 99 = rc underflow), stderr:\n%s",
				name, label, exit, stderr)
		}
		return leakSummaryOf(t, name+"/"+label, stderr)
	}
}
