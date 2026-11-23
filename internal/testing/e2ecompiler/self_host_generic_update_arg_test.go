package e2ecompiler

import "testing"

// A struct literal with a base takes the type arguments of the base. A
// generic function can give such a literal to another generic function, as
// an argument or through a local, and the call gets its instance.
const genericUpdateArgProgram = `struct Box[M] { content: string, on: Option[M] }

function inner[M](b: Box[M], w: i32): i32 {
    return b.content.len() + w;
}

function direct[M](b: Box[M], w: i32): i32 {
    return inner(Box { ...b, content: "xx" }, w);
}

function bare[M](b: Box[M], w: i32): i32 {
    return inner(Box { ...b }, w);
}

function local[M](b: Box[M], w: i32): i32 {
    let c = Box { ...b, content: "xxxx" };
    return inner(c, w);
}

function main(): i32 {
    let b: Box[i32] = Box { content: "abc", on: None };
    let s: Box[string] = Box { content: "a", on: Some("z") };
    if (direct(b, 4) != 6 || bare(b, 4) != 7 || local(b, 4) != 8) {
        return 1;
    }
    if (direct(s, 1) != 3 || inner(Box { ...s, content: "" }, 5) != 5) {
        return 2;
    }
    return 0;
}`

func TestSelfHostGenericUpdateArg(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, genericUpdateArgProgram, target, "FERN_STRICT_IR=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}
