package e2e

import "testing"

// Differential coverage for the std/http status-class predicates across
// backends: each of the five RFC 9110 classes (1xx-5xx), the combined
// http_is_error (4xx/5xx), the century boundaries, and out-of-range
// codes belonging to no class. Returns 42 iff every check holds. Each
// leg skips itself when its toolchain is absent.
const httpStatusClassProg = `
import "std/http" as http;
function main(): i32 {
    if (!http.http_is_informational(100) || http.http_is_informational(200)) { return 1; }
    if (!http.http_is_success(200) || !http.http_is_success(204) || http.http_is_success(301)) { return 2; }
    if (!http.http_is_redirect(301) || !http.http_is_redirect(302) || http.http_is_redirect(200)) { return 3; }
    if (!http.http_is_client_error(404) || http.http_is_client_error(500)) { return 4; }
    if (!http.http_is_server_error(500) || !http.http_is_server_error(503) || http.http_is_server_error(404)) { return 5; }
    if (!http.http_is_error(404) || !http.http_is_error(500) || http.http_is_error(200)) { return 6; }
    if (http.http_is_success(99) || http.http_is_server_error(600) || http.http_is_informational(0)) { return 7; }
    if (!http.http_is_success(299) || http.http_is_success(300)) { return 8; }
    if (!http.http_is_client_error(400) || !http.http_is_client_error(499)) { return 9; }
    return 42;
}
`

func TestHttpStatusClassInterp(t *testing.T) {
	if got := runInterpExit(t, httpStatusClassProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestHttpStatusClassX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, httpStatusClassProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestHttpStatusClassWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, httpStatusClassProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestHttpStatusClassArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, httpStatusClassProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
