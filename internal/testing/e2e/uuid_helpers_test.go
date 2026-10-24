package e2e

import "testing"

const uuidHelpersProg = `
import "std/uuid" as uuid;
function is(o: Option[i32], want: i32): boolean {
    if let Some(v) = o { return v == want; }
    return false;
}
function main(): i32 {
    if (uuid.uuid_nil() != "00000000-0000-0000-0000-000000000000") { return 1; }
    if (!uuid.uuid_is_nil("00000000-0000-0000-0000-000000000000")) { return 2; }
    if (uuid.uuid_is_nil("00000000-0000-0000-0000-000000000001")) { return 3; }
    if (!is(uuid.uuid_version("00000000-0000-0000-0000-000000000000"), 0)) { return 4; }
    if (!is(uuid.uuid_version("f47ac10b-58cc-4372-a567-0e02b2c3d479"), 4)) { return 5; }
    if (!is(uuid.uuid_version("018f1234-5678-7abc-9def-0123456789ab"), 7)) { return 6; }
    if (!is(uuid.uuid_version("F47AC10B-58CC-4372-A567-0E02B2C3D479"), 4)) { return 7; }
    if let Some(_) = uuid.uuid_version("not-a-uuid") { return 8; }
    if let Some(_) = uuid.uuid_version("f47ac10b58cc4372a5670e02b2c3d479") { return 9; }
    if let Some(_) = uuid.uuid_version("") { return 10; }
    if (!is(uuid.uuid_version(uuid.uuid_v4()), 4)) { return 11; }
    if (!is(uuid.uuid_version(uuid.uuid_v7()), 7)) { return 12; }
    if (uuid.uuid_is_nil(uuid.uuid_v4())) { return 13; }
    return 42;
}
`

func TestUuidHelpersInterp(t *testing.T) {
	if got := runInterpExit(t, uuidHelpersProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestUuidHelpersX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, uuidHelpersProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestUuidHelpersWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, uuidHelpersProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestUuidHelpersArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, uuidHelpersProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
