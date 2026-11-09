# Compiler gate follow-ups

Fresh main controls exposed two compiler defects and three stale expectations.
The shared audio CI branch carries these repairs so the codec branches can use one tested compiler.

Constant substitution removes global reference nodes from the checked tree.
LSP queries now recover a real constant reference from the original source when ordinary lookup finds no node.
The query retains its containing function and the qualified constant identity.
Local and parameter shadow scopes retain their existing lookup path.
Tests cover declaration sites, nested blocks, module-local references, qualified imports, hover, and definition queries.

The seccomp argument filters now permit the runtime values for UDP GRO and receive traffic classes.
The added levels are SOL_IP and SOL_UDP.
The added options are IP_RECVTOS, IPV6_RECVTCLASS, and UDP_GRO.
The syscall filter and the argument filters remain active.
The controls retain refusal of unknown levels, unknown options, and nonzero upper argument bits.

A native test enables the three options on IPv4 and IPv6 UDP sockets under the sandbox.
The same native test selects ARM64 on an ARM64 Linux host.

The constant-fold control now checks the unary AST node for a negative literal.
The freestanding platform control checks the exact MMIO capability.
The semantic graph golden now records a folded constant and the existing unboxed option paths.
A separate runtime control checks the constant value, option payloads, and retained array contents.

Issues: #14790, #14791, and #14792. Main gate: #14566. Roadmap: #14508.
