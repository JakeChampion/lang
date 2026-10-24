package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fsPathPad = 24

func fsShortPath(dir, base string) string { return filepath.Join(dir, base) }

func fsLongPath(dir, base string) string {
	return dir + "/" + strings.Repeat("./", fsPathPad) + base
}

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
            Ok(r) => { match (r.close()) { Err(e) => { return 9; }, Ok(_) => {} } },
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

func fsWriteRemoveSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(`        match (write_file("%[1]s", "payload")) { Ok(_) => {}, Err(e) => { return 8; } }
        match (remove_file("%[1]s")) { Ok(_) => {}, Err(e) => { return 9; } }`, p))
}

func fsDirTreeSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(`        match (create_dir_all("%[1]s/x/y")) { Ok(_) => {}, Err(e) => { return 8; } }
        match (remove_dir_all("%[1]s/x")) { Ok(_) => {}, Err(e) => { return 9; } }`, p))
}

func fsStatSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (stat("%s")) { Ok(st) => { acc = acc + 1; }, Err(e) => { return 8; } }`, p))
}

func fsReadDirSrc(rounds int, p string) string {
	return fsLoop(rounds, fmt.Sprintf(
		`        match (read_dir("%s")) { Ok(ns) => { acc = acc + ns.len(); }, Err(e) => { return 8; } }`, p))
}

func fsDirTreeBase(work string) string {
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
