package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The filesystem builtins past read_file / write_file build as wasm components
// with the self-host CLI (#11110). Each program runs under wasmtime and prints
// every answer, the error paths included, so an Err has to be the IoError
// variant every other target gives, and each family's ok path has to land in
// the files the program then reads back.
//
// The expected outputs are native's preview-2 component's, except where native
// is wrong: it traps reading or writing a closed Reader or Writer, and its
// remove_dir_all on a plain file leaves the file in place; x86-64 and the
// interpreter answer as these cases do.

// fsComponentPrelude names each IoError variant, and prints a Result[(), _] or
// Option[IoError] as `label ok` / `label <variant>`.
const fsComponentPrelude = `import "std/i64";
import "std/i32";

function kind(e: IoError): string {
    match (e) {
        NotFound(_) => { return "NotFound"; },
        PermissionDenied(_) => { return "PermissionDenied"; },
        AlreadyExists(_) => { return "AlreadyExists"; },
        InvalidUtf8(_) => { return "InvalidUtf8"; },
        Interrupted => { return "Interrupted"; },
        Unsupported => { return "Unsupported"; },
        Other(_, m, _) => { return "Other(" + m + ")"; }
    }
}

function unit(label: string, r: Result[(), IoError]): void {
    match (r) { Ok(_) => { print(label + " ok"); }, Err(e) => { print(label + " " + kind(e)); } }
}

function opt(label: string, r: Option[IoError]): void {
    match (r) { None => { print(label + " ok"); }, Some(e) => { print(label + " " + kind(e)); } }
}
`

var fsComponentCases = []struct {
	name, src, want string
	// noPreopen runs the component with no directory granted.
	noPreopen bool
}{
	{name: "readers and writers", src: `function main(): i32 {
    match (open_writer("f.txt")) {
        Err(e) => { print("open_writer " + kind(e)); },
        Ok(w) => {
            opt("write", w.write("one\ntwo\n"));
            match (w.write_some("three\n")) { Ok(n) => { print(f"write_some {n}"); }, Err(e) => { print("write_some " + kind(e)); } }
            opt("write_bytes", w.write_bytes([52 as u8, 10 as u8]));
            match (w.flags()) { Ok(b) => { print(f"writer flags {b}"); }, Err(e) => { print("flags " + kind(e)); } }
            match (w.stat()) { Ok(s) => { print(f"writer stat size {s.size}"); }, Err(e) => { print("stat " + kind(e)); } }
            opt("fsync", w.fsync());
            opt("fdatasync", w.fdatasync());
            opt("close", w.close());
            opt("close again", w.close());
            opt("write after close", w.write("x"));
        }
    }
    match (open_appender("f.txt")) {
        Err(e) => { print("open_appender " + kind(e)); },
        Ok(a) => {
            match (a.flags()) { Ok(b) => { print(f"appender flags {b}"); }, Err(e) => { print("flags " + kind(e)); } }
            opt("append", a.write("five\n"));
            opt("close", a.close());
        }
    }
    match (open_exclusive("f.txt")) { Ok(_) => { print("open_exclusive ok"); }, Err(e) => { print("open_exclusive " + kind(e)); } }
    match (open_reader("f.txt")) {
        Err(e) => { print("open_reader " + kind(e)); },
        Ok(r) => {
            match (r.flags()) { Ok(b) => { print(f"reader flags {b}"); }, Err(e) => { print("flags " + kind(e)); } }
            match (r.read_line()) { Some(l) => { print("line [" + l + "]"); }, None => { print("line none"); } }
            match (r.read_chunk(4)) { Ok(c) => { print("chunk [" + c + "]"); }, Err(e) => { print("chunk " + kind(e)); } }
            match (r.read_chunk_bytes(3)) { Ok(c) => { print(f"bytes {c.len()}"); }, Err(e) => { print("bytes " + kind(e)); } }
            match (r.seek(0 as i64, 0)) { Ok(p) => { print(f"seek set {p}"); }, Err(e) => { print("seek " + kind(e)); } }
            match (r.read_line()) { Some(l) => { print("again [" + l + "]"); }, None => { print("again none"); } }
            match (r.seek(-2 as i64, 2)) { Ok(p) => { print(f"seek end {p}"); }, Err(e) => { print("seek " + kind(e)); } }
            match (r.read_chunk(10)) { Ok(c) => { print("tail [" + c + "]"); }, Err(e) => { print("tail " + kind(e)); } }
            match (r.read_chunk(10)) { Ok(c) => { print("eof [" + c + "]"); }, Err(e) => { print("eof " + kind(e)); } }
            match (r.seek(-100 as i64, 1)) { Ok(p) => { print(f"seek negative {p}"); }, Err(e) => { print("seek negative " + kind(e)); } }
            match (r.stat()) { Ok(s) => { print(f"reader stat size {s.size} file {s.is_file}"); }, Err(e) => { print("stat " + kind(e)); } }
            opt("close", r.close());
            match (r.read_chunk(10)) { Ok(c) => { print("read after close [" + c + "]"); }, Err(e) => { print("read after close " + kind(e)); } }
        }
    }
    match (open_writer_with("g.txt", 1)) {
        Err(e) => { print("open_writer_with " + kind(e)); },
        Ok(w) => {
            opt("write g", w.write("abcdef"));
            opt("truncate g", w.truncate(3 as i64));
            opt("close g", w.close());
        }
    }
    match (open_reader_with("g.txt", 0)) {
        Err(e) => { print("open_reader_with " + kind(e)); },
        Ok(r) => { match (r.read_chunk(100)) { Ok(c) => { print("g [" + c + "]"); }, Err(e) => { print("g " + kind(e)); } } }
    }
    match (open_writer_with("h.txt", 0)) { Ok(_) => { print("open_writer_with no create ok"); }, Err(e) => { print("open_writer_with no create " + kind(e)); } }
    match (open_reader("missing")) { Ok(_) => { print("missing ok"); }, Err(e) => { print("missing " + kind(e)); } }
    return 0;
}`, want: `write ok
write_some 6
write_bytes ok
writer flags 2
writer stat size 16
fsync ok
fdatasync ok
close ok
close again Other(Bad file descriptor)
write after close Other(Bad file descriptor)
appender flags 6
append ok
close ok
open_exclusive AlreadyExists
reader flags 1
line [one
]
chunk [two
]
bytes 3
seek set 0
again [one
]
seek end 19
tail [e
]
eof []
seek negative Other(Invalid argument)
reader stat size 21 file true
close ok
read after close Other(Bad file descriptor)
write g ok
truncate g ok
close g ok
g [abc]
open_writer_with no create NotFound
missing NotFound
`},
	{name: "stdio handles", src: `function main(): i32 {
    let so = stdout();
    match (so.stat()) { Ok(s) => { print(f"stdout stat size {s.size} file {s.is_file}"); }, Err(e) => { print("stdout stat " + kind(e)); } }
    match (so.seek(0 as i64, 0)) { Ok(_) => { print("stdout seek ok"); }, Err(e) => { print("stdout seek " + kind(e)); } }
    opt("stdout fsync", so.fsync());
    opt("stdout truncate", so.truncate(0 as i64));
    match (so.flags()) { Ok(b) => { print(f"stdout flags {b}"); }, Err(e) => { print("stdout flags " + kind(e)); } }
    match (so.write_some_bytes([65 as u8, 10 as u8])) { Ok(n) => { print(f"write_some_bytes {n}"); }, Err(e) => { print("write_some_bytes " + kind(e)); } }
    let si = stdin();
    match (si.read_line()) { Some(l) => { print("stdin [" + l + "]"); }, None => { print("stdin none"); } }
    match (si.read_chunk(100)) { Ok(c) => { print("stdin rest [" + c + "]"); }, Err(e) => { print("stdin " + kind(e)); } }
    match (si.read_line()) { Some(l) => { print("stdin [" + l + "]"); }, None => { print("stdin eof"); } }
    match (si.flags()) { Ok(b) => { print(f"stdin flags {b}"); }, Err(e) => { print("stdin flags " + kind(e)); } }
    opt("stdin close", si.close());
    let se = stderr();
    opt("stderr close", se.close());
    opt("stderr write", se.write("lost"));
    opt("stderr close again", se.close());
    return 0;
}`, want: `stdout stat size 0 file false
stdout seek Other(Illegal seek)
stdout fsync Unsupported
stdout truncate Unsupported
stdout flags 2
A
write_some_bytes 2
stdin [first line
]
stdin rest [second
]
stdin eof
stdin flags 1
stdin close ok
stderr close ok
stderr write Other(Bad file descriptor)
stderr close again Other(Bad file descriptor)
`},
	{name: "stat and listing", src: `function count(ns: string[], want: string): i32 {
    let n: i32 = 0;
    for x in ns { if (x == want) { n = n + 1; } }
    return n;
}

function names(label: string, r: Result[string[], IoError]): void {
    match (r) {
        Ok(ns) => { print(f"{label} {ns.len()} a={count(ns, "a.txt")} b={count(ns, "b")} dot={count(ns, ".")} dotdot={count(ns, "..")}"); },
        Err(e) => { print(label + " " + kind(e)); }
    }
}

function main(): i32 {
    unit("create_dir_all", create_dir_all("d/b"));
    unit("write", write_file("d/a.txt", "hello"));
    match (stat("d/a.txt")) { Ok(s) => { print(f"stat file={s.is_file} dir={s.is_dir} size={s.size} nlink={s.nlink} ino={s.ino}"); }, Err(e) => { print("stat " + kind(e)); } }
    match (stat("d/b")) { Ok(s) => { print(f"stat dir file={s.is_file} dir={s.is_dir}"); }, Err(e) => { print("stat dir " + kind(e)); } }
    match (stat("nope")) { Ok(_) => { print("stat missing ok"); }, Err(e) => { print("stat missing " + kind(e)); } }
    unit("symlink", create_symlink("a.txt", "d/link"));
    match (lstat("d/link")) { Ok(s) => { print(f"lstat file={s.is_file} dir={s.is_dir}"); }, Err(e) => { print("lstat " + kind(e)); } }
    match (stat("d/link")) { Ok(s) => { print(f"stat link file={s.is_file} size={s.size}"); }, Err(e) => { print("stat link " + kind(e)); } }
    match (lstat("nope")) { Ok(_) => { print("lstat missing ok"); }, Err(e) => { print("lstat missing " + kind(e)); } }
    names("read_dir", read_dir("d"));
    names("read_dir_all", read_dir_all("d"));
    match (read_dir_ino("d")) {
        Ok(es) => {
            let zero: i32 = 0;
            for e in es { if (e.ino == 0 as i64) { zero = zero + 1; } }
            print(f"read_dir_ino {es.len()} ino=0 for {zero}");
        },
        Err(e) => { print("read_dir_ino " + kind(e)); }
    }
    names("read_dir file", read_dir("d/a.txt"));
    names("read_dir missing", read_dir("nope"));
    names("read_dir_all missing", read_dir_all("nope"));
    return 0;
}`, want: `create_dir_all ok
write ok
stat file=true dir=false size=5 nlink=1 ino=0
stat dir file=false dir=true
stat missing NotFound
symlink ok
lstat file=false dir=false
stat link file=true size=5
lstat missing NotFound
read_dir 3 a=1 b=1 dot=0 dotdot=0
read_dir_all 3 a=1 b=1 dot=0 dotdot=0
read_dir_ino 3 ino=0 for 3
read_dir file Other(Not a directory)
read_dir missing NotFound
read_dir_all missing NotFound
`},
	{name: "create and remove", src: `function main(): i32 {
    unit("create_dir", create_dir("d", 493));
    unit("create_dir again", create_dir("d", 493));
    unit("create_dir under missing", create_dir("x/y", 493));
    unit("create_dir_all", create_dir_all("d/b/c"));
    unit("create_dir_all again", create_dir_all("d/b/c"));
    unit("write", write_file("d/b/c/f.txt", "x"));
    unit("write top", write_file("top.txt", "x"));
    unit("remove_dir not empty", remove_dir("d/b"));
    unit("remove_dir file", remove_dir("top.txt"));
    unit("remove_file", remove_file("top.txt"));
    unit("remove_file again", remove_file("top.txt"));
    unit("remove_file dir", remove_file("d"));
    unit("remove_dir missing", remove_dir("nope"));
    unit("write top", write_file("top.txt", "x"));
    unit("remove_dir_all file", remove_dir_all("top.txt"));
    match (stat("top.txt")) { Ok(_) => { print("top.txt still there"); }, Err(e) => { print("top.txt " + kind(e)); } }
    unit("remove_dir_all", remove_dir_all("d"));
    match (stat("d")) { Ok(_) => { print("d still there"); }, Err(e) => { print("d " + kind(e)); } }
    unit("remove_dir_all missing", remove_dir_all("d"));
    unit("create_dir", create_dir("e", 493));
    unit("remove_dir", remove_dir("e"));
    return 0;
}`, want: `create_dir ok
create_dir again AlreadyExists
create_dir under missing NotFound
create_dir_all ok
create_dir_all again ok
write ok
write top ok
remove_dir not empty Other(Directory not empty)
remove_dir file Other(Not a directory)
remove_file ok
remove_file again NotFound
remove_file dir Other(Is a directory)
remove_dir missing NotFound
write top ok
remove_dir_all file ok
top.txt NotFound
remove_dir_all ok
d NotFound
remove_dir_all missing ok
create_dir ok
remove_dir ok
`},
	{name: "links and rename", src: `function main(): i32 {
    unit("write", write_file("a.txt", "hello"));
    unit("symlink", create_symlink("a.txt", "s"));
    unit("symlink exists", create_symlink("a.txt", "s"));
    unit("dangling symlink", create_symlink("nowhere", "dangle"));
    match (read_link("s")) { Ok(t) => { print("read_link " + t); }, Err(e) => { print("read_link " + kind(e)); } }
    match (read_link("dangle")) { Ok(t) => { print("read_link dangling " + t); }, Err(e) => { print("read_link dangling " + kind(e)); } }
    match (read_link("a.txt")) { Ok(t) => { print("read_link file " + t); }, Err(e) => { print("read_link file " + kind(e)); } }
    match (read_link("nope")) { Ok(t) => { print("read_link missing " + t); }, Err(e) => { print("read_link missing " + kind(e)); } }
    unit("hard link", create_link("a.txt", "h"));
    match (stat("a.txt")) { Ok(s) => { print(f"nlink {s.nlink}"); }, Err(e) => { print("stat " + kind(e)); } }
    unit("hard link exists", create_link("a.txt", "h"));
    unit("hard link missing", create_link("nope", "h2"));
    unit("rename", rename("h", "moved"));
    match (read_file("moved")) { Ok(s) => { print("moved [" + s + "]"); }, Err(e) => { print("moved " + kind(e)); } }
    unit("rename missing", rename("h", "x"));
    unit("write b", write_file("b.txt", "bye"));
    unit("rename over", rename("moved", "b.txt"));
    match (stat("moved")) { Ok(_) => { print("moved still there"); }, Err(e) => { print("moved " + kind(e)); } }
    match (read_file("b.txt")) { Ok(s) => { print("b [" + s + "]"); }, Err(e) => { print("b " + kind(e)); } }
    return 0;
}`, want: `write ok
symlink ok
symlink exists AlreadyExists
dangling symlink ok
read_link a.txt
read_link dangling nowhere
read_link file Other(Invalid argument)
read_link missing NotFound
hard link ok
nlink 2
hard link exists AlreadyExists
hard link missing NotFound
rename ok
moved [hello]
rename missing NotFound
write b ok
rename over ok
moved NotFound
b [hello]
`},
	{name: "truncate times and temp_dir", src: `function main(): i32 {
    unit("write", write_file("a.txt", "hello world"));
    unit("truncate", truncate("a.txt", 5 as i64));
    match (read_file("a.txt")) { Ok(s) => { print("after truncate [" + s + "]"); }, Err(e) => { print("read " + kind(e)); } }
    unit("truncate grow", truncate("a.txt", 7 as i64));
    match (stat("a.txt")) { Ok(s) => { print(f"size {s.size}"); }, Err(e) => { print("stat " + kind(e)); } }
    unit("truncate missing", truncate("nope", 0 as i64));
    unit("truncate dir", truncate(".", 0 as i64));
    unit("set_file_times", set_file_times("a.txt", 1000000 as i64, 5 as i64, 2000000 as i64, 7 as i64, 0));
    match (stat("a.txt")) { Ok(s) => { print(f"atime {s.atime}.{s.atime_nsec} mtime {s.mtime}.{s.mtime_nsec}"); }, Err(e) => { print("stat " + kind(e)); } }
    unit("set_file_times omit atime", set_file_times("a.txt", 0 as i64, 0 as i64, 3000000 as i64, 0 as i64, 2));
    match (stat("a.txt")) { Ok(s) => { print(f"atime {s.atime} mtime {s.mtime}"); }, Err(e) => { print("stat " + kind(e)); } }
    unit("set_file_times missing", set_file_times("nope", 0 as i64, 0 as i64, 0 as i64, 0 as i64, 0));
    match (temp_dir("tmp")) {
        Ok(p) => {
            match (stat(p)) { Ok(s) => { print(f"temp_dir dir={s.is_dir} prefix={p.starts_with("tmp-")}"); }, Err(e) => { print("temp_dir stat " + kind(e)); } }
            unit("write in temp", write_file(p + "/f", "x"));
            unit("remove temp", remove_dir_all(p));
        },
        Err(e) => { print("temp_dir " + kind(e)); }
    }
    match (temp_dir("a/b")) { Ok(_) => { print("temp_dir slash ok"); }, Err(e) => { print("temp_dir slash " + kind(e)); } }
    return 0;
}`, want: `write ok
truncate ok
after truncate [hello]
truncate grow ok
size 7
truncate missing NotFound
truncate dir Other(Is a directory)
set_file_times ok
atime 1000000.5 mtime 2000000.7
set_file_times omit atime ok
atime 1000000 mtime 3000000
set_file_times missing NotFound
temp_dir dir=true prefix=true
write in temp ok
remove temp ok
temp_dir slash Other(Invalid argument)
`},
	// Twenty open Writers outgrow the handle table's first eight slots, and a
	// closed fd is free for the next open.
	{name: "many open handles", src: `function main(): i32 {
    let ws: Writer[] = [];
    let i: i32 = 0;
    while (i < 20) {
        match (open_writer(f"f{i}.txt")) { Ok(w) => { ws = ws.append(w); }, Err(e) => { print(f"open {i} " + kind(e)); return 1; } }
        i = i + 1;
    }
    i = 0;
    while (i < 20) {
        match (ws[i].write(f"file {i}\n")) { Some(e) => { print(f"write {i} " + kind(e)); return 1; }, None => {} }
        i = i + 1;
    }
    i = 0;
    while (i < 20) {
        match (ws[i].close()) { Some(e) => { print(f"close {i} " + kind(e)); return 1; }, None => {} }
        i = i + 1;
    }
    let intact: i32 = 0;
    i = 0;
    while (i < 20) {
        match (read_file(f"f{i}.txt")) { Ok(s) => { if (s == f"file {i}\n") { intact = intact + 1; } }, Err(_) => {} }
        i = i + 1;
    }
    print(f"{intact} files intact");
    match (open_reader("f3.txt")) {
        Ok(r) => { match (r.read_line()) { Some(l) => { print("reopened [" + l + "]"); }, None => { print("reopened none"); } } },
        Err(e) => { print("reopen " + kind(e)); }
    }
    return 0;
}`, want: `20 files intact
reopened [file 3
]
`},
	// A host that preopens nothing leaves every path with nothing to resolve
	// against: NotFound, except remove_dir_all, for which a missing path is
	// already removed.
	{name: "no preopen", noPreopen: true, src: `function main(): i32 {
    match (open_reader("f")) { Ok(_) => { print("open_reader ok"); }, Err(e) => { print("open_reader " + kind(e)); } }
    match (open_writer("f")) { Ok(_) => { print("open_writer ok"); }, Err(e) => { print("open_writer " + kind(e)); } }
    match (open_appender("f")) { Ok(_) => { print("open_appender ok"); }, Err(e) => { print("open_appender " + kind(e)); } }
    match (open_reader_with("f", 0)) { Ok(_) => { print("open_reader_with ok"); }, Err(e) => { print("open_reader_with " + kind(e)); } }
    match (stat("f")) { Ok(_) => { print("stat ok"); }, Err(e) => { print("stat " + kind(e)); } }
    match (lstat("f")) { Ok(_) => { print("lstat ok"); }, Err(e) => { print("lstat " + kind(e)); } }
    match (read_dir(".")) { Ok(_) => { print("read_dir ok"); }, Err(e) => { print("read_dir " + kind(e)); } }
    match (read_dir_all(".")) { Ok(_) => { print("read_dir_all ok"); }, Err(e) => { print("read_dir_all " + kind(e)); } }
    match (read_dir_ino(".")) { Ok(_) => { print("read_dir_ino ok"); }, Err(e) => { print("read_dir_ino " + kind(e)); } }
    unit("create_dir", create_dir("d", 493));
    unit("create_dir_all", create_dir_all("d/e"));
    unit("remove_file", remove_file("f"));
    unit("remove_dir", remove_dir("d"));
    unit("remove_dir_all", remove_dir_all("d"));
    unit("rename", rename("f", "g"));
    match (read_link("f")) { Ok(_) => { print("read_link ok"); }, Err(e) => { print("read_link " + kind(e)); } }
    unit("create_symlink", create_symlink("f", "g"));
    unit("create_link", create_link("f", "g"));
    unit("truncate", truncate("f", 0 as i64));
    unit("set_file_times", set_file_times("f", 0 as i64, 0 as i64, 0 as i64, 0 as i64, 0));
    match (temp_dir("t")) { Ok(_) => { print("temp_dir ok"); }, Err(e) => { print("temp_dir " + kind(e)); } }
    return 0;
}`, want: `open_reader NotFound
open_writer NotFound
open_appender NotFound
open_reader_with NotFound
stat NotFound
lstat NotFound
read_dir NotFound
read_dir_all NotFound
read_dir_ino NotFound
create_dir NotFound
create_dir_all NotFound
remove_file NotFound
remove_dir NotFound
remove_dir_all ok
rename NotFound
read_link NotFound
create_symlink NotFound
create_link NotFound
truncate NotFound
set_file_times NotFound
temp_dir NotFound
`},
	// get-directories mints a fresh handle on every call, so asking it once per
	// operation filled the host's resource table: the read_file loop below
	// died with "resource table has no free keys" before the preopen was
	// cached.
	{name: "preopen fetched once", src: `function main(): i32 {
    unit("write", write_file("x.txt", "hello"));
    let i: i32 = 0;
    while (i < 1100000) {
        match (read_file("x.txt")) { Ok(_) => {}, Err(e) => { print("read " + kind(e)); return 1; } }
        i = i + 1;
    }
    print("read loop ok");
    return 0;
}`, want: "write ok\nread loop ok\n"},
}

func TestSelfHostWasmComponentFilesystem(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping component filesystem e2e")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range fsComponentCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, comp := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(src, []byte(fsComponentPrelude+tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", comp, src, cli.stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host component: %v\n%s", err, out)
			}
			args := []string{"run"}
			if !tc.noPreopen {
				args = append(args, "--dir", ".::.")
			}
			run := exec.Command("wasmtime", append(args, comp)...)
			run.Dir = t.TempDir()
			run.Stdin = strings.NewReader("first line\nsecond\n")
			var stdout, stderr bytes.Buffer
			run.Stdout, run.Stderr = &stdout, &stderr
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != tc.want {
				t.Errorf("stdout differs\ngot:\n%s\nwant:\n%s", got, tc.want)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr: %s", strconv.Quote(stderr.String()))
			}
		})
	}
}
