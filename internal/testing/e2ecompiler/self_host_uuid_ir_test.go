package e2ecompiler

import "testing"

func TestSelfHostUuidIR(t *testing.T) {
	cli := newStrictCLI(t)

	const validate = `import "std/uuid";
function validate(u: string, ver: i32): i32 {
    if (u.len() != 36) { return 100; }
    if (u[8] != 45 || u[13] != 45 || u[18] != 45 || u[23] != 45) { return 101; }
    if (u[14] as i32 != ver) { return 102; }
    let v: i32 = u[19] as i32;
    if (v != 56 && v != 57 && v != 97 && v != 98) { return 103; }
    return 42;
}
`
	cases := []struct {
		name string
		src  string
	}{
		{"v4", validate + `function main(): i32 { return validate(uuid.uuid_v4(), 52); }`},
		{"v7", validate + `function main(): i32 { return validate(uuid.uuid_v7(), 55); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); got != 42 {
				t.Errorf("self-host uuid %q: structural check = %d, want 42 (valid UUID)", tc.name, got)
			}
		})
	}
}
