package e2ecompiler

import "testing"

// The interpreter serves secret pages from its own heap and cannot lock them,
// so the lock answers ENOSYS. A write, a read, a wipe and the size checks act
// as they do in a compiled program.
func TestSelfHostInterpSecretPages(t *testing.T) {
	const src = `import "core/cmp";
import "std/crypto/secret";
import "std/result";

function main(): void {
  let Ok(p) = secret.new(16) else { print("new refused"); return; };
  if (p.write(14, [5u8, 6u8]).is_err()) { print("write refused"); return; }
  if (p.write(15, [5u8, 6u8]).is_ok()) { print("write past the end"); return; }
  let Ok(back) = p.read(13, 3) else { print("read refused"); return; };
  print(back == [0u8, 5u8, 6u8]);
  print(f"locked={p.locked()} errno={p.lock_errno()}");
  p.wipe();
  let Ok(zero) = p.read(14, 2) else { print("read refused"); return; };
  print(zero == [0u8, 0u8]);
  print(secret.new(0).is_err());
}
`
	const want = "true\nlocked=false errno=38\ntrue\ntrue\n"
	if got := runSelfHostInterp(t, "secret.fern", src); got != want {
		t.Fatalf("interp printed %q, want %q", got, want)
	}
}
