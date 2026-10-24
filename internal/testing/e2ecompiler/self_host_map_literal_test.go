package e2ecompiler

import "testing"

var mapLiteralCases = []struct {
	name string
	src  string
	exit int
}{
	{"i32-keys", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 1: 10, 2: 20, 3: 12 }; match (m.get(2)) { Some(v) => { return v; }, None => { return 0; } } }", 20},
	{"string-keys", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = Map { \"a\": 40, \"b\": 2 }; let t: i32 = 0; match (m.get(\"a\")) { Some(x) => { t = t + x; }, None => {} } match (m.get(\"b\")) { Some(x) => { t = t + x; }, None => {} } return t; }", 42},
	{"len-has", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 7: 1, 8: 2 }; if (m.len() == 2 && m.contains(8) && !m.contains(9)) { return 9; } return 0; }", 9},
	{"empty", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { }; m = m.insert(5, 42); match (m.get(5)) { Some(v) => { return v; }, None => { return 0; } } }", 42},
	{"trailing-comma-i32", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 1: 10, 2: 20, }; return m.len() * 10 + m.get_or(2, 0); }", 40},
	{"trailing-comma-string", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = Map { \"a\": 40, \"b\": 2, }; return m.get_or(\"a\", 0) + m.get_or(\"b\", 0); }", 42},
	{"no-trailing-comma-control", "import \"core/map\"; function main(): i32 { let m: Map[i32,i32] = Map { 1: 10, 2: 20 }; return m.len() * 10 + m.get_or(2, 0); }", 40},
}

func TestSelfHostMapLiteralX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapLiteralCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostMapLiteralArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapLiteralCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
