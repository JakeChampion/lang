package e2ecompiler

import "testing"

var recursionIRCases = []struct {
	name string
	main string
}{
	{"fact", "function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }\nfunction main(): i32 { return fact(5); }"},
	{"fib", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\nfunction main(): i32 { return fib(10); }"},
	{"tail-acc", "function sum_to(n: i32, acc: i32): i32 { if (n == 0) { return acc; } return sum_to(n - 1, acc + n); }\nfunction main(): i32 { return sum_to(10, 0); }"},
	{"mutual", "function ping(n: i32): i32 { if (n <= 0) { return 0; } return 1 + pong(n - 1); }\nfunction pong(n: i32): i32 { if (n <= 0) { return 0; } return 1 + ping(n - 1); }\nfunction main(): i32 { return ping(7); }"},
	{"gcd", "function gcd(a: i32, b: i32): i32 { if (b == 0) { return a; } return gcd(b, a - (a / b) * b); }\nfunction main(): i32 { return gcd(48, 36); }"},
	{"ipow", "function ipow(base: i32, e: i32): i32 { if (e == 0) { return 1; } return base * ipow(base, e - 1); }\nfunction main(): i32 { return ipow(2, 6); }"},
	{"ackermann", "function ack(m: i32, n: i32): i32 { if (m == 0) { return n + 1; } if (n == 0) { return ack(m - 1, 1); } return ack(m - 1, ack(m, n - 1)); }\nfunction main(): i32 { return ack(2, 3); }"},
}

func TestSelfHostRecursionIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range recursionIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
