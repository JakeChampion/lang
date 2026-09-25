package x86_64ssa

// The socket family behind std/async and the edge-handler examples: raw fds
// in, raw fds or -errno out, as the flat backend's __fern_tcp_* routines and
// arm64ssa's helpers report them. Linux x86-64: socket 41, connect 42,
// accept 43, bind 49, listen 50, poll 7.

// emitTcpListenHelper writes tcp_listen(port) -> i32: an AF_INET TCP listener
// bound to 0.0.0.0:port, its fd or -errno. The sockaddr_in lives in the
// frame; the port is byte-swapped into network order. rbx = fd.
func emitTcpListenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_listen"))
	w("\tpush rbx")
	w("\tsub rsp, 16") // sockaddr_in; one push and this keep rsp 16-aligned
	w("\tmov eax, edi")
	w("\txchg al, ah")                // htons(port)
	w("\tmov word ptr [rsp], 2")      // sin_family = AF_INET
	w("\tmov word ptr [rsp + 2], ax") // sin_port
	w("\tmov dword ptr [rsp + 4], 0") // sin_addr = 0.0.0.0
	w("\tmov qword ptr [rsp + 8], 0") // sin_zero
	w("\tmov edi, 2")                 // AF_INET
	w("\tmov esi, 1")                 // SOCK_STREAM
	w("\txor edx, edx")
	w("\tmov eax, 41") // socket
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcpl_ret")
	w("\tmov ebx, eax")
	w("\tmov edi, ebx")
	w("\tmov rsi, rsp")
	w("\tmov edx, 16")
	w("\tmov eax, 49") // bind
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcpl_close")
	w("\tmov edi, ebx")
	w("\tmov esi, 128")
	w("\tmov eax, 50") // listen
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcpl_close")
	w("\tmov eax, ebx")
	w("\tjmp .Lssa_tcpl_ret")
	w(".Lssa_tcpl_close:")
	w("\tmov qword ptr [rsp], rax") // sockaddr is dead; preserve errno
	w("\tmov edi, ebx")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov rax, qword ptr [rsp]")
	w(".Lssa_tcpl_ret:")
	w("\tadd rsp, 16")
	w("\tpop rbx")
	w("\tret")
}

// emitTcpConnectHelper writes tcp_connect(host_be, port) -> i32: a connected
// AF_INET socket's fd or -errno. host_be is the IPv4 address already in
// network byte order packed into an i32, so it drops straight into sin_addr.
// rbx = fd.
func emitTcpConnectHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_connect"))
	w("\tpush rbx")
	w("\tsub rsp, 16")
	w("\tmov eax, esi")
	w("\txchg al, ah") // htons(port)
	w("\tmov word ptr [rsp], 2")
	w("\tmov word ptr [rsp + 2], ax")
	w("\tmov dword ptr [rsp + 4], edi") // sin_addr
	w("\tmov qword ptr [rsp + 8], 0")
	w("\tmov edi, 2")
	w("\tmov esi, 1")
	w("\txor edx, edx")
	w("\tmov eax, 41") // socket
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcpc_ret")
	w("\tmov ebx, eax")
	w("\tmov edi, ebx")
	w("\tmov rsi, rsp")
	w("\tmov edx, 16")
	w("\tmov eax, 42") // connect
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcpc_close")
	w("\tmov eax, ebx")
	w("\tjmp .Lssa_tcpc_ret")
	w(".Lssa_tcpc_close:")
	w("\tmov qword ptr [rsp], rax") // sockaddr is dead; preserve errno
	w("\tmov edi, ebx")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov rax, qword ptr [rsp]")
	w(".Lssa_tcpc_ret:")
	w("\tadd rsp, 16")
	w("\tpop rbx")
	w("\tret")
}

// emitTcpAcceptHelper writes tcp_accept(fd) -> i32: accept(2) with no peer
// address, the connection's fd or -errno. Leaf.
func emitTcpAcceptHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_accept"))
	w("\txor esi, esi")
	w("\txor edx, edx")
	w("\tmov eax, 43") // accept
	w("\tsyscall")
	w("\tret")
}

// emitTcpLocalPortHelper writes tcp_local_port(fd) -> i32: getsockname(2) into
// a frame sockaddr_in, answering the bound port in host order or -errno. The
// port the kernel chose for a tcp_listen(0) is only readable this way. Leaf.
func emitTcpLocalPortHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_local_port"))
	w("\tsub rsp, 24")                  // sockaddr_in at [rsp], socklen_t at [rsp+16]
	w("\tmov dword ptr [rsp + 16], 16") // addrlen = sizeof(sockaddr_in)
	w("\tmov rsi, rsp")
	w("\tlea rdx, [rsp + 16]")
	w("\tmov eax, 51") // getsockname
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcplp_ret")
	w("\tmovzx eax, word ptr [rsp + 2]") // sin_port, network order
	w("\txchg al, ah")                   // ntohs
	w(".Lssa_tcplp_ret:")
	w("\tadd rsp, 24")
	w("\tret")
}

// emitTcpRecvHelper writes tcp_recv(fd, max) -> u8[]: up to max bytes from
// the socket into a fresh u8[] whose len is what arrived; EOF, an error and a
// non-positive max all leave it empty. rbx = fd, r12 = data.
func emitTcpRecvHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_recv"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 8") // two pushes and one slot keep rsp 16-aligned
	w("\tmov ebx, edi")
	w("\tmov edi, esi")
	w("\ttest edi, edi")
	w("\tjg .Lssa_tcpr_alloc")
	w("\txor edi, edi")
	w(".Lssa_tcpr_alloc:")
	w("\tcall %s", fnLabel("__alloc_u8"))
	w("\tmov r12, rax")
	w("\tmov edx, %s", memRef("r12", -12)) // cap = max
	w("\ttest edx, edx")
	w("\tjz .Lssa_tcpr_empty")
	w("\tmov edi, ebx")
	w("\tmov rsi, r12")
	w("\txor eax, eax") // read
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjg .Lssa_tcpr_len")
	w(".Lssa_tcpr_empty:")
	w("\txor eax, eax")
	w(".Lssa_tcpr_len:")
	w("\tmov %s, eax", memRef("r12", -4))
	w("\tmov rax, r12")
	w("\tadd rsp, 8")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitTcpSendHelper sends with MSG_NOSIGNAL, returning accepted bytes or -errno.
func emitTcpSendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_send"))
	w("\tmov edx, %s", memRef("rsi", -4)) // len; fd and data are in place
	w("\tmov r10d, 16384")                // MSG_NOSIGNAL
	w("\txor r8d, r8d")
	w("\txor r9d, r9d")
	w("\tmov eax, 44") // sendto
	w("\tsyscall")
	w("\tret")
}

// emitUdpSendHelper writes udp_send(host, port, data) -> i32: one datagram to
// a dotted-quad IPv4 literal, the bytes sent or -errno, and -3 for a host that
// is not four decimal octets. On a datagram socket connect only records the
// peer, so connect-then-write sends it. The octets are parsed straight into
// sin_addr. rbx = fd, r12 = data, r13 = port, then the result.
func emitUdpSendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("udp_send"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tsub rsp, 16")
	w("\tmov r12, rdx")
	w("\tmov r13d, esi")
	w("\tmov ecx, %s", memRef("rdi", -4)) // host length
	w("\txor r8d, r8d")                   // index into host
	w("\txor r9d, r9d")                   // the octet so far
	w("\txor r10d, r10d")                 // octets completed
	w("\txor r11d, r11d")                 // digits in this octet
	w(".Lssa_udps_scan:")
	w("\tcmp r8d, ecx")
	w("\tjge .Lssa_udps_end")
	w("\tmovzx eax, byte ptr [rdi + r8]")
	w("\tcmp eax, 46") // '.'
	w("\tjne .Lssa_udps_digit")
	w("\ttest r11d, r11d")
	w("\tjz .Lssa_udps_bad")
	w("\tcmp r10d, 3")
	w("\tjge .Lssa_udps_bad")
	w("\tmov byte ptr [rsp + r10 + 4], r9b")
	w("\tinc r10d")
	w("\txor r9d, r9d")
	w("\txor r11d, r11d")
	w("\tinc r8d")
	w("\tjmp .Lssa_udps_scan")
	w(".Lssa_udps_digit:")
	w("\tsub eax, 48")
	w("\tcmp eax, 9")
	w("\tja .Lssa_udps_bad") // unsigned: below '0' wraps high too
	w("\timul r9d, r9d, 10")
	w("\tadd r9d, eax")
	w("\tinc r11d")
	w("\tcmp r9d, 255")
	w("\tjg .Lssa_udps_bad")
	w("\tinc r8d")
	w("\tjmp .Lssa_udps_scan")
	w(".Lssa_udps_end:")
	w("\tcmp r10d, 3")
	w("\tjne .Lssa_udps_bad")
	w("\ttest r11d, r11d")
	w("\tjz .Lssa_udps_bad")
	w("\tmov byte ptr [rsp + 7], r9b")
	w("\tmov word ptr [rsp], 2") // AF_INET
	w("\tmov eax, r13d")
	w("\txchg al, ah") // htons(port)
	w("\tmov word ptr [rsp + 2], ax")
	w("\tmov qword ptr [rsp + 8], 0")
	w("\tmov edi, 2")
	w("\tmov esi, 2") // SOCK_DGRAM
	w("\txor edx, edx")
	w("\tmov eax, 41") // socket
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_udps_ret")
	w("\tmov ebx, eax")
	w("\tmov edi, ebx")
	w("\tmov rsi, rsp")
	w("\tmov edx, 16")
	w("\tmov eax, 42") // connect
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_udps_close")
	w("\tmov edi, ebx")
	w("\tmov rsi, r12")
	w("\tmov edx, %s", memRef("r12", -4))
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w(".Lssa_udps_close:")
	w("\tmov r13, rax") // the result, kept across close
	w("\tmov edi, ebx")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov rax, r13")
	w("\tjmp .Lssa_udps_ret")
	w(".Lssa_udps_bad:")
	w("\tmov eax, -3")
	w(".Lssa_udps_ret:")
	w("\tadd rsp, 16")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitTcpCloseHelper writes tcp_close(fd) -> i32: close(2), 0 or -errno. Leaf.
func emitTcpCloseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_close"))
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tret")
}

// emitConstHelper returns the emitter for a helper that answers a constant
// whatever its arguments: the wasm pollable surface on native, where a
// socket's readiness token IS its fd, a deadline has no pollable (-1, which
// poll ignores) and dropping one is nothing.
func emitConstHelper(name string, value int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov eax, %d", value)
		w("\tret")
	}
}

// emitIdentityHelper returns the emitter for a helper that hands its first
// argument back: tcp_pollable(fd) on native.
func emitIdentityHelper(name string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov rax, rdi")
		w("\tret")
	}
}

// emitPollHelper writes poll(fds, timeout_ms) -> i32: the index of the first
// fd in the i32[] that is readable, or -1 on a timeout or none. A transient
// pollfd[] (8 bytes each: fd, events, revents) is bump-allocated, every entry
// asks for POLLIN, and poll(2) takes the millisecond timeout directly (a
// negative one blocks). rbx = fds, r12 = pollfd[], r13 = nfds, r14 =
// timeout.
func emitPollHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("poll"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tsub rsp, 8") // four pushes and one slot keep rsp 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r14d, esi")
	w("\tmov r13d, %s", memRef("rbx", -4)) // nfds
	w("\txor r12d, r12d")
	w("\ttest r13d, r13d")
	w("\tjle .Lssa_poll_none")
	w("\tlea rdi, [r13 * 8]")
	w("\tcall %s", fnLabel("__alloc"))
	w("\tmov r12, rax")
	w("\txor ecx, ecx")
	w(".Lssa_poll_fill:")
	w("\tcmp ecx, r13d")
	w("\tjge .Lssa_poll_filled")
	w("\tmov eax, [rbx + rcx * 4]")
	w("\tmov [r12 + rcx * 8], eax")             // .fd
	w("\tmov dword ptr [r12 + rcx * 8 + 4], 1") // .events = POLLIN, .revents = 0
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_poll_fill")
	w(".Lssa_poll_filled:")
	w("\tmov rdi, r12")
	w("\tmov esi, r13d")
	w("\tmovsxd rdx, r14d")
	w("\tmov eax, 7") // poll
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjle .Lssa_poll_none")
	w("\txor ecx, ecx")
	w(".Lssa_poll_scan:")
	w("\tcmp ecx, r13d")
	w("\tjge .Lssa_poll_none")
	w("\ttest word ptr [r12 + rcx * 8 + 6], 1") // revents & POLLIN
	w("\tjnz .Lssa_poll_found")
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_poll_scan")
	w(".Lssa_poll_found:")
	w("\tmov eax, ecx")
	w("\tjmp .Lssa_poll_ret")
	w(".Lssa_poll_none:")
	w("\tmov eax, -1")
	w(".Lssa_poll_ret:")
	w("\tmov r14d, eax")
	w("\ttest r12, r12")
	w("\tjz .Lssa_poll_restore")
	w("\tmov rdi, r12")
	w("\tlea rsi, [r13 * 8]")
	w("\tcall %s", fnLabel("__free"))
	w(".Lssa_poll_restore:")
	w("\tmov eax, r14d")
	w("\tadd rsp, 8")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
