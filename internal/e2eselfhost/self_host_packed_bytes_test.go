package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A u8[] the typed lowering builds is packed a byte an element on every
// backend (#9634, #10987), where it had an 8-byte slot per byte on the
// register backends and a 4-byte one on wasm: every element op, push, slice
// and allocation carries the byte stride, and each runtime helper that makes
// or reads a u8[] works on the packed bytes. Each program below runs on all
// three targets and must match the native compiler's output with the heap
// balanced.

// packedBytesElementsSrc: literals, pushes past a grow, `.with`, slices,
// string bytes both ways, __alloc_u8, random_bytes, a digest over a string
// that crosses many blocks, and the heap cost of a 4 MiB byte array. Packed
// it is 5 MiB after the large tier's rounding; a 4-byte slot per byte would
// round to 20 MiB and an 8-byte one to 40 MiB, so the 12 MB threshold
// separates packed from unpacked on every target.
const packedBytesElementsSrc = `import "std/i32";
import "std/crypto";

function total(bs: [u8]): i32 {
    var t: i32 = 0;
    for b in bs { t = t + (b as i32); }
    return t;
}

function main(): i32 {
    var lit: u8[] = [1 as u8, 2 as u8, 250 as u8];
    print("lit " + lit.len().to_string() + " " + total(lit).to_string());
    var grown: u8[] = [];
    var i: i32 = 0;
    while (i < 1000) { grown = grown.append((i % 256) as u8); i = i + 1; }
    print("grown " + grown.len().to_string() + " " + total(grown).to_string() + " " + (grown[999] as i32).to_string());
    grown = grown.with(3, 77 as u8);
    var w: u8[] = grown.with(4, 88 as u8);
    print("with " + (grown[3] as i32).to_string() + " " + (w[4] as i32).to_string() + " " + (grown[4] as i32).to_string());
    var sl: [u8] = grown[10:20];
    print("slice " + sl.len().to_string() + " " + total(sl).to_string());
    var s: string = "hello, world";
    var b: u8[] = s.bytes();
    print("bytes " + b.len().to_string() + " " + total(b).to_string() + " " + string_from_bytes_unchecked(b));
    print("as_bytes " + total(s.as_bytes()).to_string());
    var z: u8[] = __alloc_u8(13);
    z = z.with(12, 9 as u8);
    print("alloc " + z.len().to_string() + " " + total(z).to_string());
    print("random " + random_bytes(37).len().to_string());
    var big: string = "abcdefghijklmnop";
    var k: i32 = 0;
    while (k < 12) { big = big + big; k = k + 1; }
    print(crypto.sha256_hex(big));
    var before: i64 = __heap_bump_bytes();
    var mb: u8[] = __alloc_u8(4194304);
    var cost: i64 = __heap_bump_bytes() - before;
    print("heap " + (mb.len() > 0 && cost < (12000000 as i64)).to_string());
    return 0;
}
`

const packedBytesElementsWant = "lit 3 253\ngrown 1000 124716 231\nwith 77 88 4\nslice 10 145\n" +
	"bytes 12 1160 hello, world\nas_bytes 1160\nalloc 13 9\nrandom 37\n" +
	"22fb1d9f8b2574684a11d8fa40d94d55cabfcac2d327d9373491be51ad3be467\nheap true"

// packedBytesContainersSrc: a u8[] as a map's keys and values (the runtime
// map's snapshot is repacked), a generic callee, a record field grown in
// place, nested arrays, a tuple, an Option and a for-in rebuild.
const packedBytesContainersSrc = `import "std/i32";
import "std/array";
import "core/map";

struct Rec { tag: u8, data: u8[] }

function sum(bs: u8[]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < bs.len()) { t = t * 31 + (bs[i] as i32); i = i + 1; }
    return t;
}

function main(): i32 {
    var m: Map[u8, i32] = Map {};
    m = m.insert(3 as u8, 30);
    m = m.insert(200 as u8, 2000);
    m = m.insert(7 as u8, 70);
    print("keys " + m.keys().len().to_string() + " " + sum(m.keys()).to_string());
    var n: Map[string, u8] = Map {};
    n = n.insert("a", 5 as u8);
    n = n.insert("b", 250 as u8);
    print("vals " + sum(n.values()).to_string());
    var a: u8[] = [1 as u8, 2 as u8, 3 as u8];
    var r: u8[] = array.reverse(a);
    print("rev " + sum(r).to_string());
    var c: Rec = Rec { tag: 9 as u8, data: [4 as u8, 5 as u8] };
    c = Rec { ...c, data: c.data.append(6 as u8) };
    print("rec " + (c.tag as i32).to_string() + " " + sum(c.data).to_string());
    var nested: u8[][] = [a, r];
    print("nested " + sum(nested[1]).to_string());
    var t: (u8, u8[]) = (7 as u8, a);
    print("tuple " + (t.0 as i32).to_string() + " " + sum(t.1).to_string());
    var o: Option[u8[]] = Some(r);
    match (o) { Some(x) => { print("opt " + sum(x).to_string()); }, None => { print("none"); } }
    var acc: u8[] = [];
    for x in r { acc = acc.append(x); }
    print("iter " + sum(acc).to_string());
    return 0;
}
`

const packedBytesContainersWant = "keys 3 9090\nvals 405\nrev 2946\nrec 9 4005\nnested 2946\ntuple 7 1026\nopt 2946\niter 2946"

// packedBytesRuntimeSrc: the buffer pushes that read a u8[] table (mapped,
// filtered, expanded, and a table shorter than the bytes it maps), the two
// set scans, read_file_bytes, and a write_file_bytes round trip long enough
// that the read grows its buffer.
const packedBytesRuntimeSrc = `import "std/i32";
import "std/io_buffered";

function table(n: i32, f: (i32) => i32): u8[] {
    var t: u8[] = [];
    var i: i32 = 0;
    while (i < n) { t = t.append(f(i) as u8); i = i + 1; }
    return t;
}

function main(): i32 {
    var upper: u8[] = table(256, (c: i32) => { if (c >= 97 && c <= 122) { return c - 32; } return c; });
    var drop: u8[] = table(128, (c: i32) => { if (c == 111 || c == 32) { return 1; } return 0; });
    var exp: u8[] = table(2048, (i: i32) => {
        var c: i32 = i / 8;
        var k: i32 = i % 8;
        if (c == 44) { if (k == 0) { return 3; } if (k == 1) { return 60; } if (k == 2) { return 45; } if (k == 3) { return 62; } }
        if (k == 0) { return 1; }
        if (k == 1) { return c; }
        return 0;
    });
    var b: io_buffered.BufWriter = io_buffered.buf_writer_new(stdout(), 4096);
    b = b.write_mapped("hello, world\n", upper);
    b = b.write_filtered("foo bar boo\n", drop);
    b = b.write_expanded("a,b,c\n", exp);
    b = b.write_mapped("abc\n", [0 as u8, 1 as u8]);
    b = b.flush();
    buf_free(b.handle());
    var ws: u8[] = table(256, (c: i32) => { if (c == 32 || c == 10) { return 1; } return 0; });
    var line: string = "one two  three\nfour";
    print("scan " + __scan_set(line, 0, ws).to_string() + " " + __scan_set(line, 4, ws).to_string() + " " + __scan_set(line, 15, ws).to_string());
    print("runs " + __count_runs(line, 0, ws).to_string());
    match (temp_dir("pk")) {
        Ok(dir) => {
            var path: string = dir + "/bytes.bin";
            write_file(path, "\x01\x02xyz\xff");
            match (read_file_bytes(path)) {
                Ok(bs) => {
                    var t: i32 = 0;
                    for x in bs { t = t * 7 + (x as i32); }
                    print("file " + bs.len().to_string() + " " + t.to_string());
                },
                Err(e) => { print("file err"); }
            }
            var big: u8[] = [];
            var j: i32 = 0;
            while (j < 10000) { big = big.append((j % 251) as u8); j = j + 1; }
            var big_path: string = dir + "/big.bin";
            match (write_file_bytes(big_path, big)) { Ok(_) => {}, Err(e) => { print("big write err"); } }
            match (read_file_bytes(big_path)) {
                Ok(bs) => {
                    var t: i32 = 0;
                    for x in bs { t = (t * 31 + (x as i32)) % 1000003; }
                    print("big " + bs.len().to_string() + " " + t.to_string());
                },
                Err(e) => { print("big read err"); }
            }
            remove_dir_all(dir);
        },
        Err(e) => { print("temp err"); }
    }
    return 0;
}
`

const packedBytesRuntimeWant = "HELLO, WORLD\nfbarb\na<->b<->c\nabc\nscan 3 7 19\nruns 3\nfile 6 69807\nbig 10000 270744"

// packedBytesConstSrc: a constant u8[] literal whose length is not a
// multiple of 8, appended to on every evaluation. The constant has no spare
// capacity, so each append copies; with room in its padding the first append
// grew the constant in place and every later evaluation read the byte and
// the length it had written (#10647).
const packedBytesConstSrc = `import "std/i32";

function mk(): u8[] { return [1, 2, 3]; }

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var a: u8[] = mk().append(9);
        t = t + a.len();
        i = i + 1;
    }
    var b: u8[] = mk().with(0, 7);
    print("const " + t.to_string() + " " + mk().len().to_string() + " " + (b[0] as i32).to_string() + " " + (mk()[0] as i32).to_string());
    return 0;
}
`

const packedBytesConstWant = "const 12 3 7 1"

// packedBytesComponentSrc: the runtime case's write_file_bytes round trip,
// built as a wasm component, whose preview-2 read_file_bytes gathers the
// stream's 4096-byte chunks into the array and grows it twice on the way.
const packedBytesComponentSrc = `import "std/i32";

function main(): i32 {
    var big: u8[] = [];
    var j: i32 = 0;
    while (j < 10000) { big = big.append((j % 251) as u8); j = j + 1; }
    match (write_file_bytes("big.bin", big)) { Ok(_) => {}, Err(e) => { print("big write err"); } }
    match (read_file_bytes("big.bin")) {
        Ok(bs) => {
            var t: i32 = 0;
            for x in bs { t = (t * 31 + (x as i32)) % 1000003; }
            print("big " + bs.len().to_string() + " " + t.to_string());
        },
        Err(e) => { print("big read err"); }
    }
    return 0;
}
`

const packedBytesComponentWant = "big 10000 270744"

func TestSelfHostPackedBytes(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src, want string }{
		{"elements", packedBytesElementsSrc, packedBytesElementsWant},
		{"containers", packedBytesContainersSrc, packedBytesContainersWant},
		{"runtime", packedBytesRuntimeSrc, packedBytesRuntimeWant},
		{"constant", packedBytesConstSrc, packedBytesConstWant},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1")
					got := strings.ReplaceAll(stdout, "\n\n", "\n")
					if got != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", got, c.want, stderr)
					}
					if target == "x86-64-linux" && !strings.Contains(stderr, "leakcheck:") {
						t.Fatalf("no leak census line\n%s", stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") || (strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0")) {
						t.Fatalf("heap finding:\n%s", stderr)
					}
				})
			}
		})
	}
	t.Run("component", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime not on PATH")
		}
		dir := t.TempDir()
		in := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(in, []byte(packedBytesComponentSrc), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "prog.wasm")
		compile := exec.Command(selfHostBin, "-target", "wasm32-wasi", in, stdlibRoot, "-o", out)
		compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1")
		if msg, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		var stdout, stderr strings.Builder
		run := exec.Command("wasmtime", "run", "--dir", dir+"::.", out)
		run.Stdout, run.Stderr = &stdout, &stderr
		if err := run.Run(); err != nil {
			t.Fatalf("run: %v\n%s", err, stderr.String())
		}
		if got := strings.TrimSpace(stdout.String()); got != packedBytesComponentWant {
			t.Fatalf("stdout = %q, want %q\n%s", got, packedBytesComponentWant, stderr.String())
		}
		if strings.Contains(stderr.String(), "fern-sanitizer:") {
			t.Fatalf("heap finding:\n%s", stderr.String())
		}
	})
}
