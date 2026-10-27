package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostQualifiedTypeVariantHeads(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, tc := range []struct{ name, source string }{
		{"nullary", `enum Direction { North, South }
enum Problem { Direction, Bad }
function choose(flag: boolean): Direction { return if (flag) { Direction.North } else { Direction.South() }; }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 if (match (choose(true)) { North => false, _ => true }) { return 1; }
 return match (choose(false)) { South => 0, _ => 2 };
}`},
		{"generic-payload", `enum Packet[T] { Item(T), End }
enum Problem { Packet, Bad }
function make(flag: boolean): Packet[string] { return if (flag) { Packet.Item(args()[0] + "-owned-payload-for-qualified-constructors") } else { Packet.End }; }
function main(): i32 {
 match (make(true)) { Item(value) => { if (value.len() < 30) { return 1; } }, End => { return 2; } }
 return match (make(false)) { End => 0, _ => 3 };
}`},
		{"multiple-owners", `enum Direction { North, South }
enum Left { Direction(string), Other }
enum Right { Direction, Different }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 let direction = Direction.North;
 let payload = Left.Direction(seed + "-left-payload-is-owned");
 if (match (payload) { Left.Direction(value) => value.len() < 20, _ => true }) { return 1; }
 return match (direction) { North => 0, _ => 2 };
}`},
		{"local-shadow", `enum Direction { North, South }
enum Problem { Direction, Bad }
struct Holder { North: i32 }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 let Direction = Holder { North: 17 };
 return Direction.North - 17;
}`},
		{"parameter-shadow", `enum Direction { North, South }
enum Problem { Direction, Bad }
struct Holder { North: i32 }
function read(Direction: Holder): i32 { return Direction.North; }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 return read(Holder { North: 19 }) - 19;
}`},
		{"captured-shadow", `enum Direction { North, South }
enum Problem { Direction, Bad }
struct Holder { North: i32 }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 let Direction = Holder { North: 23 };
 let read: () => i32 = (): i32 => Direction.North;
 return read() - 23;
}`},
		{"constructor-value-method", `enum State { Ready, Other }
function (value: State) code(): i32 { return match (value) { Ready => 7, Other => 9 }; }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 return Ready.code() - 7;
}`},
		{"constructor-before-struct-method", `enum State { Ready, Other }
struct Ready { number: i32 }
function (value: State) code(): i32 { return match (value) { Ready => 7, Other => 9 }; }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 let record: Ready = Ready { number: 11 };
 return Ready.code() + record.number - 18;
}`},
		{"struct-before-constructor-method", `struct Ready { number: i32 }
enum State { Ready, Other }
function (value: State) code(): i32 { return match (value) { Ready => 7, Other => 9 }; }
function main(): i32 {
 let seed = args()[0]; if (seed.len() == 0) { return 90; }
 let record: Ready = Ready { number: 11 };
 return Ready.code() + record.number - 18;
}`},
		{"owned-record-payload", `struct Payload { bytes: u8[] }
enum Choice { Node(Payload), Empty }
enum Problem { Choice, Bad }
function main(): i32 {
 let bytes = __alloc_u8(args()[0].len() + 33);
 let retained = bytes;
 let value = Choice.Node(Payload { bytes: bytes });
 bytes = bytes.with(0, 7 as u8);
 return match (value) { Node(payload) => if (payload.bytes.len() == retained.len() && payload.bytes[0] == 0 as u8 && bytes[0] == 7 as u8) { 0 } else { 1 }, Empty => 2 };
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(oracle, "-interp", path).CombinedOutput(); err != nil {
				t.Fatalf("Go interpreter: %v\n%s", err, out)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("qualified type or value exit %d\n%s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func TestSelfHostQualifiedTypeVariantHeadDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	codes := regexp.MustCompile(`error\[E[0-9]+\]`)
	const header = "enum Direction { North, South }\nenum Problem { Direction, Bad }\n"
	for _, tc := range []struct{ name, source, code string }{
		{"missing-member", header + `function main(): i32 { let d = Direction.Missing; return 0; }`, "E036"},
		{"wrong-nullary-arity", header + `function main(): i32 { let d = Direction.North(1); return 0; }`, "E036"},
		{"nullary-arity-without-collision", `enum Direction { North, South }
function main(): i32 { let d = Direction.North(1); return 0; }`, "E036"},
		{"unqualified-nullary-arity", header + `function main(): i32 { let d = North(1); return 0; }`, "E038"},
		{"qualified-payload-value", `enum Packet[T] { Item(T), End }
enum Problem { Packet, Bad }
function main(): i32 { let value = Packet.Item; return 0; }`, "E036"},
		{"unqualified-payload-value", `enum Packet { Item(string), End }
function main(): i32 { let value = Item; return 0; }`, "E036"},
		{"unqualified-ambiguous-value", `enum Direction { North, South }
enum Left { Direction(string), Other }
enum Right { Direction, Different }
function main(): i32 { let d = Direction; return 0; }`, "E036"},
		{"ambiguous-argument-to-qualified-constructor", `enum Direction { North(i32), South }
enum Left { Shared, Other }
enum Right { Shared, Different }
function main(): i32 { let d = Direction.North(Shared); return 0; }`, "E036"},
		{"wrong-payload", `enum Packet[T] { Item(T), End }
enum Problem { Packet, Bad }
function make(): Packet[string] { return Packet.Item(7); }
function main(): i32 { return 0; }`, "E002"},
		{"local-shadow-missing-field", header + `struct Holder { Other: i32 }
function main(): i32 { let Direction = Holder { Other: 1 }; return Direction.North; }`, "E043"},
		{"parameter-shadow-missing-field", header + `struct Holder { Other: i32 }
function read(Direction: Holder): i32 { return Direction.North; }
function main(): i32 { return 0; }`, "E043"},
		{"declared-type-wins-over-constructor-method", header + `function (value: Problem) code(): i32 { return 7; }
function main(): i32 { return Direction.code() - 7; }`, "E036"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, goErr := exec.Command(oracle, "-check", path).CombinedOutput()
			if goErr == nil || !strings.Contains(string(goOut), "error["+tc.code+"]") {
				t.Fatalf("Go oracle expected %s: %v\n%s", tc.code, goErr, goOut)
			}
			wantCodes := strings.Join(codes.FindAllString(string(goOut), -1), ",")
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					gotCodes := strings.Join(codes.FindAllString(string(out), -1), ",")
					if err == nil || gotCodes != wantCodes {
						t.Fatalf("expected diagnostic sequence %s, got %s: %v\n%s", wantCodes, gotCodes, err, out)
					}
				})
			}
		})
	}
}
