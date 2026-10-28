Found in full CI run `40620158743` for #13718 at `c63e488b4ed21b7161417382b8e4f7f6f05a5da5`.
`FuzzSelfHostArm64Assembler` used 2,148,569,088 resident bytes on 8,135 input bytes, above its unchanged 512 MiB budget.
CI did not publish the minimized seed as an artifact.

A separate 31-byte local input reproduces the same memory failure class:

```asm
.text
134217728:
 b 134217728b
```

The current assembler used 2,098,212 KiB and then aborted with `allocation size out of range`.
Smaller label values of one and two million used 17,060 KiB and 33,060 KiB.
`arm64_gas_nl_grow` creates a dense counter array indexed by the numeric label value.
A large label therefore controls memory use independently of input size.

Use sparse counters for the labels that actually occur.
Preserve repeated definitions, forward and backward references, and unresolved-target refusal.
Normalize decimal label names without integer overflow.
Retain the original fuzz budgets and add a permanent memory regression plus exact branch-offset controls.
The repair will remain on #13718 with the other CI fixes.
