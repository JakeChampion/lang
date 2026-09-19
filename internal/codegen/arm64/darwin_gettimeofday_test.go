package arm64

import (
	"regexp"
	"strings"
	"testing"
)

// XNU's `gettimeofday` takes THREE arguments where libc exposes two:
//
//	int gettimeofday(struct timeval *tp, struct timezone *tzp,
//	                 uint64_t *mach_absolute_time)
//
// The kernel writes the machine's absolute time through the third when it is
// non-null, and the third is whatever the caller happened to leave in x2. A
// stale x2 pointing anywhere writable is an eight-byte store into it — a heap
// corruption that surfaces later and somewhere else entirely. It took `pr -`
// out inside `__fern_alloc`, one allocation after the clock was read, and
// `base32` and `base64` with it (#9799).
//
// Textual, because the semantics need an x2 that happens to hold a writable
// address: whether a given program crashes depends on what the caller last
// put there, so a behavioural test would pass on the bug most of the time.
// What is checkable is the instruction, at every site that issues the call.
const gettimeofdayClockSrc = `function main(): i32 {
    var a: i64 = now_unix_ms();
    var b: i64 = now_ns();
    return (a - b) as i32;
}`

// The zeroing must sit between the buffer setup and the `svc`, so the check is
// on the ORDER and not merely on the instruction being somewhere in the body.
var gettimeofdayCall = regexp.MustCompile(`(?s)mov x2, #0.{0,120}?mov x16, #116.{0,40}?svc #0x80`)

func TestArm64DarwinGettimeofdayZeroesTheThirdArgument(t *testing.T) {
	asm := compile(t, gettimeofdayClockSrc, Options{Darwin: true})
	for _, helper := range []string{"__fern_now_unix_ms", "__fern_now_ns"} {
		body := helperBody(asm, helper)
		if body == "" {
			t.Fatalf("%s not emitted; the test cannot guard a helper that is absent", helper)
		}
		if !strings.Contains(body, "mov x16, #116") {
			t.Fatalf("%s does not issue gettimeofday; this test is checking the wrong helper", helper)
		}
		if !gettimeofdayCall.MatchString(body) {
			t.Errorf("arm64-darwin %s issues gettimeofday without zeroing x2 first:\n%s\n"+
				"the kernel writes mach_absolute_time through it when it is non-null, so a stale x2 "+
				"is an eight-byte store into whatever the caller left there", helper, body)
		}
	}
}

// set_file_times reads the clock for a now bit, and there x2 is a LIVE operand
// the helper saves across the syscall — so the zeroing has to come after the
// save and the restore has to come after the call, or the fix would cost the
// operand it protects.
func TestArm64DarwinSetFileTimesZeroesTheThirdArgument(t *testing.T) {
	asm := compile(t, setFileTimesSrc, Options{Darwin: true})
	body := helperBody(asm, "__fern_set_file_times")
	if body == "" {
		t.Fatal("__fern_set_file_times not emitted; the test cannot guard a helper that is absent")
	}
	if !gettimeofdayCall.MatchString(body) {
		t.Errorf("arm64-darwin __fern_set_file_times issues gettimeofday without zeroing x2 first")
	}
	save := strings.Index(body, "stp x2, x3, [x29, #96]")
	zero := strings.Index(body, "mov x2, #0")
	restore := strings.Index(body, "ldp x2, x3, [x29, #96]")
	svc := strings.Index(body, "svc #0x80")
	if save < 0 || zero < 0 || restore < 0 || svc < 0 {
		t.Fatalf("__fern_set_file_times no longer has the save / zero / call / restore shape:\n%s", body)
	}
	if !(save < zero && zero < svc && svc < restore) {
		t.Errorf("__fern_set_file_times orders save=%d zero=%d svc=%d restore=%d; the zeroing must "+
			"land after the operand is saved and before the call, and the restore after it", save, zero, svc, restore)
	}
}

// Linux reaches the wall clock through clock_gettime, which takes two
// arguments and no third pointer. The zeroing must not turn up there — it
// would be an instruction with nothing to protect, and a reader would take it
// for a requirement of the syscall.
func TestArm64LinuxClockHelpersHaveNoThirdArgument(t *testing.T) {
	asm := compile(t, gettimeofdayClockSrc, Options{})
	for _, helper := range []string{"__fern_now_unix_ms", "__fern_now_ns"} {
		body := helperBody(asm, helper)
		if body == "" {
			t.Fatalf("%s not emitted", helper)
		}
		if strings.Contains(body, "mov x16, #116") {
			t.Errorf("arm64-linux %s issues a Darwin BSD syscall", helper)
		}
	}
}
