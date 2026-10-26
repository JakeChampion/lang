package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
)

func TestSelfHostFmtLambdaListGroups(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `enum Status { Cancelled, Other }
enum Fault[E] { Decode(Status), Consumer(E) }
function produce(input: i32, limit: i32, cancel: () => boolean, state: i32, consume: (i32) => Result[i32, string]): Result[i32, Fault[string]] {
 if (cancel()) { return Err(Decode(Cancelled)); }
 return Ok(7);
}
function main(): i32 {
 for n in [0, 1] {
  let observed = cell_new(0);
  let baseline = produce(n, 2, (): boolean => { observed.set(observed.get() + 1); return false; }, 0, (value: i32): Result[i32, string] => Ok(value));
  match (baseline) { Ok(_) => {}, Err(_) => { return 1; } }
  for stop in 1..observed.get() + 1 {
   let polls = cell_new(0);
   let result = produce(n, 2, (): boolean => { polls.set(polls.get() + 1); return polls.get() == stop; }, 0, (value: i32): Result[i32, string] => Ok(value));
   match (result) { Err(Decode(Status.Cancelled())) => {}, _ => { return 2; } }
  }
 }
 return 0;
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("source check: %v\n%s", err, out)
	}
	for pass := 0; pass < 2; pass++ {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
			t.Fatalf("format pass %d: %v\n%s", pass, err, out)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if pass > 0 && string(before) != string(after) {
			t.Fatal("second format changed the source")
		}
	}
	original, err := goparser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := goprinter.Format(original); string(formatted) != want {
		t.Fatalf("printer mismatch\n--- Go ---\n%s\n--- Fern ---\n%s", want, formatted)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("formatted program exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostFmtLambdaListLayouts(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, source := range []string{
		`function main(): i32 { return call(1, 2, (): i32 => { let n = 1; return n; }, 3, (): i32 => 4); }`,

		`function main(): i32 { let work = (): i32 => call((): i32 => { let n = 1; return n; }, 2); return work(); }`,
		`function main(): i32 { let work = (): i32 => { let value = call((): i32 => { let n = 1; return n; }, 2); return value; }; return work(); }`,
		`function main(): i32 { let work = (): void => { call((): i32 => { let n = 1; return n; }, 2); }; return 0; }`,

		`function main(): i32 { return call((): i32 => { let n = 1; return n; }, 2); }`,
		`function main(): i32 { return call(
(): i32 => { let n = 1; return n; }, 2,
); }`,
		`function main(): i32 { let callbacks = [(): i32 => { let n = 1; return n; }, (): i32 => 2]; return 0; }`,
		`function main(): i32 { let value = Callbacks { first: (): i32 => { let n = 1; return n; }, second: (): i32 => 2 }; return 0; }`,
		`function main(): i32 { return call((): i32 => { let n = 1; return n; },
// second argument
2); }`,
		`function main(): i32 { return call((): i32 => { let n = 1; return n; }, (): i32 => { let n = 2; return n; }, 3); }`,
		`function main(): i32 { return call((point @ P { x }: P): i32 => { let n = point.x; return n + x; }, 2); }`,
	} {
		t.Run(source, func(t *testing.T) {
			written, err := goparser.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			want := goprinter.Format(written)
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				if out, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", "-w", path).CombinedOutput(); err != nil {
					t.Fatalf("format pass %d: %v\n%s", pass, err, out)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("printer mismatch on pass %d\n--- Go ---\n%s\n--- Fern ---\n%s", pass, want, got)
				}
				back, err := goparser.Parse(string(got))
				if err != nil {
					t.Fatal(err)
				}
				if goprinter.ASTShape(written) != goprinter.ASTShape(back) {
					t.Fatal(goprinter.FirstShapeDiff(goprinter.ASTShape(written), goprinter.ASTShape(back)))
				}
			}
		})
	}
}
