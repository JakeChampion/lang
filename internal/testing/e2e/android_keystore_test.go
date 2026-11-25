package e2e

import "testing"

// A nonzero sentinel JNIEnv makes an accidental JNI call fail on the host.
// Invalid inputs must return before looking up Java classes or allocating output.
func TestAndroidKeystoreInputBounds(t *testing.T) {
	const program = `import "std/android/keystore";
import "std/jni";

function refused(r: Result[u8[], jni.JavaError]): boolean {
  return match (r) { Err(_) => true, Ok(_) => false };
}

function main(): i32 {
  let e = 1 as usize;
  if (!refused(keystore.seal(e, "k", []))) { return 1; }
  if (!refused(keystore.seal(e, "k", __alloc_u8(4097)))) { return 2; }
  if (!refused(keystore.open(e, "k", __alloc_u8(28)))) { return 3; }
  if (!refused(keystore.open(e, "k", __alloc_u8(4125)))) { return 4; }
  if (!refused(keystore.open(e, "", __alloc_u8(29)))) { return 5; }
  if (!refused(keystore.seal(0 as usize, "k", [1u8]))) { return 6; }
  if (!refused(keystore.seal_with_context(e, "k", [], [1u8]))) { return 7; }
  if (!refused(keystore.seal_with_context(e, "k", __alloc_u8(4097), [1u8]))) { return 8; }
  if (!refused(keystore.open_with_context(e, "k", __alloc_u8(28), [1u8]))) { return 9; }
  if (!refused(keystore.open_with_context(e, "k", __alloc_u8(4125), [1u8]))) { return 10; }
  return 0;
}`
	if _, got := compileAndRunX86_64(t, program); got != 0 {
		t.Fatalf("input validation exited with status %d", got)
	}
}

// Invalid signing-key inputs must return before any JNI call.
func TestAndroidKeystoreSigningInputBounds(t *testing.T) {
	const program = `import "std/android/keystore";
import "std/jni";

function refused(r: Result[keystore.SigningKey, jni.JavaError]): boolean {
  return match (r) { Err(_) => true, Ok(_) => false };
}

function main(): i32 {
  let e = 1 as usize;
  let o = keystore.key_options();
  if (!refused(keystore.make_signing_key(e, "k", [], o))) { return 1; }
  if (!refused(keystore.make_signing_key(e, "k", __alloc_u8(129), o))) { return 2; }
  if (!refused(keystore.make_signing_key(e, "", [1u8], o))) { return 3; }
  if (!refused(keystore.make_signing_key(0 as usize, "k", [1u8], o))) { return 4; }
  if (!refused(keystore.signing_key(e, ""))) { return 5; }
  if (keystore.sign(e, "k", __alloc_u8(1048577)).is_ok()) { return 6; }
  if (keystore.sign(e, "", [1u8]).is_ok()) { return 7; }
  if (keystore.delete_key(e, "").is_ok()) { return 8; }
  return 0;
}`
	if _, got := compileAndRunX86_64(t, program); got != 0 {
		t.Fatalf("signing input validation exited with status %d", got)
	}
}
