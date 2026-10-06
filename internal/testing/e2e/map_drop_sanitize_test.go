package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A Map handle is released by core/map's __map_drop_impl, which hands the
// shared case to __fern_rc_dec and the last reference to __fern_box_free.
// These pin that the runtime's detectors still see a map handle through it:
// an over-release is reported, and a freed handle is quarantined so a second
// drop is a use-after-free rather than a silent write into a recycled block.

// mapOverReleaseSrc drops a handle whose count is already zero.
const mapOverReleaseSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    __store_i32(h - 8, 0);
    __map_drop_impl(h);
    return __rc_underflow_count();
}`

// mapDoubleDropSrc drops the same handle twice; the first drop frees it.
const mapDoubleDropSrc = `import "core/map";
function main(): i32 {
    let h: usize = map_new_impl(4, 0, 0);
    __map_drop_impl(h);
    __map_drop_impl(h);
    return 7;
}`

func checkMapDropFinding(t *testing.T, stderr string, code, want int, finding string) {
	t.Helper()
	if code != want {
		t.Errorf("exit=%d, want %d", code, want)
	}
	if !strings.Contains(stderr, finding) {
		t.Errorf("stderr does not name %q: %q", finding, stderr)
	}
	if !strings.Contains(stderr, "backtrace:") {
		t.Errorf("stderr carries no backtrace: %q", stderr)
	}
}

func TestX86_64SanitizeMapHandleOverRelease(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, mapOverReleaseSrc)
	checkMapDropFinding(t, stderr, code, e2eharness.ExitSanitizer, "fern-sanitizer: rc over-release (double free)")
}

// Unsanitized, the over-release is counted rather than fatal.
func TestX86_64MapHandleOverReleaseCounted(t *testing.T) {
	_, stderr, code := runPlain(t, e2eharness.TargetX86_64Linux, mapOverReleaseSrc)
	if code != 1 || stderr != "" {
		t.Errorf("exit=%d stderr=%q, want exit 1 (one over-release counted) and no report", code, stderr)
	}
}

func TestX86_64SanitizeMapHandleUseAfterFree(t *testing.T) {
	_, stderr, code := runSanitizeX86_64(t, mapDoubleDropSrc)
	checkMapDropFinding(t, stderr, code, e2eharness.ExitSanitizer, "fern-sanitizer: use-after-free (touched a quarantined block)")
}

func TestArm64SanitizeMapHandleOverRelease(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, mapOverReleaseSrc)
	checkMapDropFinding(t, stderr, code, e2eharness.ExitSanitizer, "fern-sanitizer: rc over-release (double free)")
}

func TestArm64SanitizeMapHandleUseAfterFree(t *testing.T) {
	_, stderr, code := runSanitizeArm64(t, mapDoubleDropSrc)
	checkMapDropFinding(t, stderr, code, e2eharness.ExitSanitizer, "fern-sanitizer: use-after-free (touched a quarantined block)")
}
