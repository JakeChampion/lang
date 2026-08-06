package wasmbin

// The module's static memory map: the reserved low-memory scratch window,
// the freelist heads table, and the static pools above it.
//
// EVERY address here is DERIVED — each slot starts where the previous one
// ends, and each region starts where the previous region ends. Nothing in
// this file is a hand-picked number except the single base and the slot
// SIZES, which is the point: the map is a chain, so two consumers cannot
// name the same bytes.
//
// They used to be hand-picked `const …Addr = N` values spread across
// runtime.go and wasi.go, and four addresses were claimed twice (#6229):
// __fern_print's iovec sat on the rc-underflow counter and the heap-base
// seed, its fd_write nwritten slot sat on the append-cliff counter, and the
// cached instance-network borrow sat on the cliff's weight accumulator. Each
// pair's comment asserted sole ownership and neither mentioned the other. So
// `__heap_bump_bytes()` — which CLAUDE.md names as *the* way to measure a
// Fern program's memory — returned garbage on wasm after any print, and both
// rc diagnostics could report a large non-zero on a healthy run or be zeroed
// back out on a broken one.
//
// That is the same failure mode as #6142, where the closure-cell pool grew
// into the freelist heads; the regions above the table were made derived
// then, and this finishes the job for the scratch window below it.

// scratchBase is where the reserved window starts. Address 0 is unusable as
// a heap pointer anyway (every rc helper treats it as null), so the scratch
// costs nothing.
const scratchBase = 0

// The scratch slots, in address order. Each `+ N` is the PREVIOUS slot's
// size in bytes — that is the whole layout mechanism.
const (
	// Cache for __fern_arg_at / __fern_env_at. Both helpers lazily
	// initialise on first call: ask the host for sizes, alloc the pointer
	// table + string buffer, call args_get / environ_get, store the
	// (count, table_ptr) in the cache. Subsequent calls short-circuit on
	// the init flag and walk the cached table.
	argsInitAddr      = scratchBase           // args_init flag (0 / 1)
	argsCountAddr     = argsInitAddr + 4      // argc
	argsPtrsAddr      = argsCountAddr + 4     // argv_ptrs heap pointer
	argsSizesArgcAddr = argsPtrsAddr + 4      // argc out from args_sizes_get
	argsSizesBufAddr  = argsSizesArgcAddr + 4 // bufsize out from args_sizes_get
	envInitAddr       = argsSizesBufAddr + 4  // env_init flag (0 / 1)
	envCountAddr      = envInitAddr + 4       // env count
	envPtrsAddr       = envCountAddr + 4      // environ_ptrs heap pointer
	envSizesArgcAddr  = envPtrsAddr + 4       // count out from environ_sizes_get
	envSizesBufAddr   = envSizesArgcAddr + 4  // bufsize out from environ_sizes_get

	// allocCursorAddr holds the bump cursor: the i32 LE pointer to the next
	// free byte, seeded in wasmbin.go to max(allocMinStart, end-of-string-
	// pool) rounded up to 8.
	allocCursorAddr = envSizesBufAddr + 4

	// readByteScratchAddr holds the HEAP pointer to __fern_read_byte's
	// per-call scratch region (iovec + 1-byte buffer + nread out). 0 means
	// uninitialised; the helper allocs 16 bytes on first call and writes the
	// address here so subsequent calls reuse the same region.
	readByteScratchAddr = allocCursorAddr + 4

	// printIovecAddr is where __fern_print writes the (iov_base, iov_len)
	// pair before calling fd_write — 8 bytes, base at +0 and len at +4.
	printIovecAddr = readByteScratchAddr + 4

	// printRetAddr is where fd_write writes its nwritten result. The
	// preview-1 byte-write shim deliberately reuses this pair as a 1-byte
	// buffer plus descriptor (see buildWriteByteBody) — that is one helper
	// family sharing its own slots, not a second claimant.
	printRetAddr = printIovecAddr + 8

	// randomBufAddr is where wasi_random_get writes the bytes
	// __fern_random_i32 consumes.
	randomBufAddr = printRetAddr + 4

	// strIdxScratchAddr is the spill region __str_idx uses for inline-form
	// strings: base_data at +0, base_len at +4, and __str_idx returns
	// scratch+i so the caller's OpLoadByte reads the correct content byte.
	// Heap-form strings bypass it entirely (the returned address is
	// base_data+i).
	strIdxScratchAddr = randomBufAddr + 4

	// networkHandleInitAddr / networkHandleAddr cache the
	// wasi:sockets/instance-network borrow tcp_listen's start-bind step
	// consumes. The handle is an opaque i32 where 0 is VALID, which is why
	// it needs a separate init flag to mean "not yet fetched" — and that
	// flag is what the cliff-weight accumulator used to overwrite.
	networkHandleInitAddr = strIdxScratchAddr + 8
	networkHandleAddr     = networkHandleInitAddr + 4

	// stdoutInitAddr / stdoutHandleAddr cache the
	// wasi:cli/stdout::get-stdout() own<output-stream> handle the preview-2
	// print helper consumes; stderr's pair is the same shape for
	// wasi:cli/stderr. init=0 means "not yet fetched"; on first call the
	// helper invokes get-stdout, stores the handle, and sets init=1.
	stdoutInitAddr   = networkHandleAddr + 4
	stdoutHandleAddr = stdoutInitAddr + 4
	stderrInitAddr   = stdoutHandleAddr + 4
	stderrHandleAddr = stderrInitAddr + 4

	// mapHashSeedAddr holds core/map's per-process string-hash seed (#6194).
	// Zero means "not yet drawn"; __fern_map_hash_seed forces the drawn value
	// nonzero so the slot doubles as its own cache flag — and so a seed of 0,
	// which is core/map's "unseeded" sentinel, can never reach a map header.
	mapHashSeedAddr = stderrHandleAddr + 4

	// rcUnderflowAddr is the rc-underflow detector: buildRcDecBody bumps it
	// whenever __fern_rc_dec is asked to decrement an rc that is already
	// <= 0 — a value that has been over-released, which under Phase-3
	// reclamation is a use-after-free. Read back by __rc_underflow_count so
	// tests can assert it stays 0 across a program, which is why a print
	// writing an iov_base over it was worse than useless.
	rcUnderflowAddr = mapHashSeedAddr + 4

	// heapBaseAddr holds the bump cursor's SEED (its position at program
	// start), written by the same data segment that seeds the cursor.
	// __fern_heap_bump_bytes returns (cursor − heapBase), so it reads 0 at
	// start and grows only on fresh bumps — matching the natives'
	// (heap_ptr − heap_base).
	heapBaseAddr = rcUnderflowAddr + 4

	// arrPushSharedAddr is the rc==1 append-cliff counter: buildArrPushGrowBody
	// bumps it whenever it takes the COPY path on a buffer that still had
	// spare capacity, so the copy was bought by an extra reference rather
	// than by a full buffer. Read back by __arr_push_shared_count.
	arrPushSharedAddr = heapBaseAddr + 4

	// arrPushCopiedAddr is the same cliff WEIGHTED by bytes — oldLen * stride
	// accumulated per crossing, read back by __arr_push_shared_bytes. i64,
	// because the quantity is arena-scale: the count alone cannot rank
	// crossing sites, since a whole-module self-host compile crosses 188
	// times and copies 812 bytes doing it while one threaded accumulator over
	// 20k appends copies 2.3 GB.
	arrPushCopiedAddr = arrPushSharedAddr + 4

	// scratchEnd is the first address past the named scratch.
	scratchEnd = arrPushCopiedAddr + 8
)

// allocMinStart is the floor for the bump cursor: past every reserved slot
// above. On the native preview-2 layout the cursor is seeded well beyond it
// (at the end of the string pool), so this only binds on layouts with no
// static data at all.
const allocMinStart = scratchEnd

// freelistHeadsAddr is the base of the Phase 3 step-4 segregated freelist:
// `freelistClasses` i32 heads starting at the first 8-aligned address past
// the scratch. Linear memory is zero-initialised, so every class starts
// empty. Only consulted when ast.RcFreeEnabled; the flag-off allocator never
// touches it.
//
// Derived rather than fixed at 256: the comment on the old constant claimed
// the scratch "tops out at 92", which was already wrong when mapHashSeedAddr
// took 96, and would have gone on being wrong for every slot added after.
const freelistHeadsAddr = (scratchEnd + 7) &^ 7

// Freelist class geometry. The heads table holds `freelistClasses` i32 slots
// at freelistHeadsAddr:
//
//	0..127     small tier — 16-byte EXACT-FIT classes; slot i is the
//	           freelist for blocks of size (i+1)*16, i.e. 16..2048 B.
//	128..191   large tier — 64 slots, four per octave (capacities
//	           rounded to 3 significant bits), covering 2 KiB up to
//	           2^25 B (32 MiB). Blocks above that are not recycled.
//
// The large tier is the wasm mirror of the native two-tier freelist
// (#3425). Without it every buffer over 2048 B was dropped on the
// floor: a `string[]` self-append loop's largest grow buffer is
// 16 + 8*cap bytes, which crosses 2048 at cap 254 — so
// `a = a.append(s)` past ~254 elements leaked one buffer per call,
// while the single-word `struct[]` sibling (16 + 4*cap) stayed under
// the ceiling and reclaimed fine. That asymmetry is what made the
// gap look string-specific rather than size-specific.
const (
	freelistSmallClasses = 128
	freelistLargeClasses = 64
	freelistClasses      = freelistSmallClasses + freelistLargeClasses
	// Largest size the small tier covers, and the granularity of its
	// exact-fit classes.
	freelistSmallMax = freelistSmallClasses * 16 // 2048
)

// freelistHeadsEnd is the first address past the heads table — and the
// base of everything static that follows it.
const freelistHeadsEnd = freelistHeadsAddr + freelistClasses*4

// The static data regions above the freelist table, each starting where
// the previous one ends. Two regions once BOTH claimed [96, 1024) — the
// closure-cell pool grew up from 96 with a (1024-96)/8 = 116-cell budget,
// while the freelist heads owned [256, 1024) — so a program with more than
// 20 unique OpConstFunc targets wrote cells straight over the heads table.
// The allocator then popped a function index as a freelist head and handed
// back a pointer into low memory, which the program wrote through, and the
// clobbered bump cursor eventually produced a head that trapped on
// dereference (#6142). The reverse write is just as real: a free stores a
// heap pointer into a head slot that a `call_indirect` later reads as a
// function index ("undefined element").
const (
	// closuresBase is the static OpConstFunc closure-pair pool:
	// maxClosureCells 8-byte { fn_idx (i32 LE), env_ptr=0 (i32 LE) } cells,
	// addressed as closuresBase + 8*tableIdx.
	closuresBase    = freelistHeadsEnd
	maxClosureCells = 116
	closurePoolEnd  = closuresBase + 8*maxClosureCells

	// stringStart is where heap-form string literals begin; the bump
	// cursor is seeded past the end of that pool, so every heap
	// allocation lands above all of the static regions.
	stringStart = closurePoolEnd
)

// rcLowAddrGuard is the address floor the rc helpers use to skip static
// closure cells (which have no rc header at [ptr-8]) without skipping real
// heap objects. The bump cursor is seeded at max(allocMinStart,
// end-of-string-pool) and the string pool starts at stringStart, so every
// heap allocation lands at or above it while every static region — scratch,
// freelist heads, closure cells — lands below. That makes stringStart the
// exact threshold, which is why this tracks it rather than restating it.
//
// Both sides previously used 0x10000 (64 KiB), carried over from a WASI
// memory layout whose heap sat above 64 KiB. On the native preview-2
// layout (heap at ~1024) that silently skipped EVERY rc op: inc never
// bumped a refcount (so an aliasing `var y = x` left x at rc==1 and
// `y.set(...)` took the mutate-in-place CoW fast path, corrupting x),
// and dec/free never reclaimed (so the freelist stayed empty and alloc
// was a pure bump).
//
// The floor is stringStart for both inc and the dec/free/reclamation
// helpers (__fern_rc_dec / __fern_arr_dec / __fern_drop_arr_ptr /
// __fern_map_drop / __fern_box_free / __fern_rc_is_unique). It is the
// correct skip threshold on every layout: it skips null + every static
// region while catching every real heap object. The WASI/adapter layout
// (heap above 64 KiB) is unaffected — its objects clear both thresholds.
const rcLowAddrGuard = stringStart

// Build-time proof that nothing overlaps: a region that started before its
// predecessor ended would make one of these negative, and a negative
// constant does not convert to uint. They are the backstop for the parts of
// the map that are NOT pure summation — the 8-byte alignment step into the
// freelist table, and the two pools above it.
//
// There is deliberately no assertion over the scratch slots themselves.
// Each is defined as the previous one's address plus the previous one's
// size, so an overlap there is not a condition to detect — it is
// unspellable.
const (
	freelistClearsScratch     = uint(freelistHeadsAddr - scratchEnd)
	closurePoolClearsFreelist = uint(closuresBase - freelistHeadsEnd)
	stringPoolClearsClosures  = uint(stringStart - closurePoolEnd)
	cursorClearsScratch       = uint(allocMinStart - scratchEnd)
)
