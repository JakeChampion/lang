package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestArm64DarwinReplacedZoneLocal runs #9165's reproducer: a struct local
// conditionally replaced, then handed to a separate function with a wide
// string-carrying struct live beside it. On arm64-darwin the replacing branch
// segfaulted where the other did not, which is how `date -u` crashed. The
// imports are coreutils' own, so the program sits beside a link to
// coreutils/lib.
func TestArm64DarwinReplacedZoneLocal(t *testing.T) {
	const prog = `import "std/string";
import "std/array";
import "std/io_buffered";
import "./lib/gnu";
import "./lib/timefmt";
import "./lib/tz";

struct Scan { date_text: string, has_date: boolean, file: string, has_file: boolean, reference: string, has_reference: boolean, format: string, has_format: boolean, utc: boolean, debug: boolean }

function declare_options(): gnu.Opt[] {
  var opts: gnu.Opt[] = [];
  opts = opts.append(gnu.valued("d", "date", 1));
  opts = opts.append(gnu.flag("", "debug", 2));
  opts = opts.append(gnu.valued("f", "file", 3));
  opts = opts.append(gnu.optional("I", "iso-8601", 4));
  opts = opts.append(gnu.valued("r", "reference", 8));
  opts = opts.append(gnu.flag("R", "rfc-email", 6));
  opts = opts.append(gnu.valued("", "rfc-3339", 7));
  opts = opts.append(gnu.flag("", "resolution", 5));
  opts = opts.append(gnu.valued("s", "set", 9));
  opts = opts.append(gnu.flag("u", "utc", 10));
  opts = opts.append(gnu.flag("", "universal", 10));
  return opts;
}

function scan_options(): (Scan, string[]) {
  var s: Scan = Scan { date_text: "", has_date: false, file: "", has_file: false, reference: "", has_reference: false, format: "", has_format: false, utc: false, debug: false };
  var g: gnu.Getopt = gnu.getopt_new(args(), declare_options(), "date", "help\n", 1);
  while (true) {
    var res: (Option[gnu.OptMatch], gnu.Getopt) = g.next();
    g = res.1;
    match (res.0) {
      Some(m) => {
        if (m.id == 10) {
          s = Scan { ...s, utc: true };
        }
      },
      None => {
        break;
      }
    }
  }
  return (s, g.operands());
}

struct Stamp { sec: i64, nsec: i64 }

function now_stamp(): Stamp {
  var n: i64 = now_ns();
  var s: i64 = n / (1000000000 as i64);
  var r: i64 = n % (1000000000 as i64);
  if (r < 0 as i64) {
    s = s - 1 as i64;
    r = r + 1000000000 as i64;
  }
  return Stamp { sec: s, nsec: r };
}

function emit(o: io_buffered.BufWriter, fmt: string, z: tz.Zone, st: Stamp): io_buffered.BufWriter {
  return gnu.put(o, timefmt.strftime(fmt, z, st.sec, st.nsec) + "\n");
}

function main(): i32 {
  var scanned: (Scan, string[]) = scan_options();
  var s: Scan = scanned.0;
  var operands: string[] = scanned.1;

  var fmt: string = "%a %b %e %H:%M:%S %Z %Y";
  var z: tz.Zone = tz.local_zone();
  if (s.utc) {
    z = tz.utc_zone();
  }
  var now: Stamp = now_stamp();
  var o: io_buffered.BufWriter = gnu.out_new();
  var st: Stamp = now;
  o = emit(o, fmt, z, st);
  gnu.finish(o);
  return 0;
}
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	lib, err := filepath.Abs("../../coreutils/lib")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lib, filepath.Join(dir, "lib")); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	stamp := regexp.MustCompile(`^[A-Z][a-z]{2} [A-Z][a-z]{2} [ 0-9][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2} UTC [0-9]{4}\n$`)
	for _, args := range [][]string{nil, {"-u"}} {
		cmd := exec.Command(out, args...)
		cmd.Env = append(os.Environ(), "TZ=UTC")
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		if !stamp.Match(got) {
			t.Errorf("run %v printed %q, want a UTC date line", args, got)
		}
	}
}
