package e2eharness

// The exit statuses a compiled program's runtime reserves for its own
// findings. Both are clear of the 128+signal range, so no signal death can
// forge them, and distinct from each other, so the two are told apart by
// status alone. The self-host emitters hold their own copies in Fern source;
// TestArenaExhaustedExitCodeSelfHostLockstep (internal/testing/e2e) scans them
// against ExitArenaExhausted, and the sanitizer tests observe ExitSanitizer
// at run time.
const (
	// ExitSanitizer is a fatal sanitizer finding (FERN_SANITIZE).
	ExitSanitizer = 124
	// ExitArenaExhausted is __fern_alloc's arena-exhaustion abort.
	ExitArenaExhausted = 125
)
