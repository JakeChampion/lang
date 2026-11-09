    .globl _start
    .text
__fn_main:
    .cfi_startproc
__fn_main.r:
    pushq %rbx
    .cfi_def_cfa_offset 16
    .cfi_offset %rbx, -16
    pushq %r12
    .cfi_def_cfa_offset 24
    .cfi_offset %r12, -24
    pushq %r13
    .cfi_def_cfa_offset 32
    .cfi_offset %r13, -32
    pushq %r14
    .cfi_def_cfa_offset 40
    .cfi_offset %r14, -40
    pushq %rcx
    .cfi_def_cfa_offset 48
.Lssa_main_0:
.Lssa_main_6:
    leaq .SB0(%rip), %rsi
.Lssa_main_5:
    movq %rsi, %rbx
    xorl %r12d, %r12d
.Lssa_main_7:
    cmpq $10, %r12
    jge .Lssa_main_3
.Lssa_main_8:
    pushq %rbx
    pushq %rbx
    call __fn___fern_str_grow
    addq $16, %rsp
    testq %rax, %rax
    jz .Lssa_main_14
.Lssa_main_13:
    jmp .Lssa_main_15
.Lssa_main_14:
    pushq %rbx
    pushq %rbx
    call __fn___fern_str_grow_fresh
    addq $16, %rsp
    movq %rax, %r13
    testb $1, %bl
    jnz .Lssa_main_rcdeck47
    cmpq $0x10000, %rbx
    jb .Lssa_main_rcdeck47
    cmpl $1, -8(%rbx)
    jg .Lssa_main_rcdecd47
    cmpl $0, -8(%rbx)
    js .Lssa_main_rcdeck47
    movq %rbx, %rax
    call __fn___fern_str_free.u
    jmp .Lssa_main_rcdece47
.Lssa_main_rcdecd47:
    subl $1, -8(%rbx)
.Lssa_main_rcdeck47:
.Lssa_main_rcdece47:
    movq %r13, %rbx
.Lssa_main_15:
    addq $1, %r12
    cmpq $10, %r12
    jl .Lssa_main_8
.Lssa_main_3:
.Lssa_main_2:
    xorl %r12d, %r12d
    xorl %r13d, %r13d
.Lssa_main_18:
    cmpq $6000, %r13
    jl .Lssa_main_19
.Lssa_main_21:
    testb $1, %bl
    jnz .Lssa_main_rcdeck65
    cmpq $0x10000, %rbx
    jb .Lssa_main_rcdeck65
    cmpl $1, -8(%rbx)
    jg .Lssa_main_rcdecd65
    cmpl $0, -8(%rbx)
    js .Lssa_main_rcdeck65
    movq %rbx, %rax
    call __fn___fern_str_free.u
    jmp .Lssa_main_rcdece65
.Lssa_main_rcdecd65:
    subl $1, -8(%rbx)
.Lssa_main_rcdeck65:
.Lssa_main_rcdece65:
    jmp .Lssa_main_1
.Lssa_main_19:
    movl $97, %esi
    movq %rsi, %rcx
    movq %rbx, %rax
    movq 8(%rax), %r8
    movq (%rax), %rax
    xorq %rdx, %rdx
    xorq %r10, %r10
    cmpq $255, %rcx
    ja .L3
    movd %ecx, %xmm1
    punpcklbw %xmm1, %xmm1
    punpcklwd %xmm1, %xmm1
    pshufd $0, %xmm1, %xmm1
    vpbroadcastb %xmm1, %ymm1
.L6:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $128, %r9
    jl .L4
    cmpq $1024, %r9
    jl .L7
    leaq 1024(%rdx), %r11
    vpxor %ymm2, %ymm2, %ymm2
.L8:
    vpcmpeqb (%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 32(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 64(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 96(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    addq $128, %rdx
    cmpq %r11, %rdx
    jl .L8
    vpxor %ymm3, %ymm3, %ymm3
    vpsadbw %ymm3, %ymm2, %ymm2
    subq $32, %rsp
    vmovdqu %ymm2, (%rsp)
    addq (%rsp), %r10
    addq 8(%rsp), %r10
    addq 16(%rsp), %r10
    addq 24(%rsp), %r10
    addq $32, %rsp
    jmp .L6
.L7:
    vpcmpeqb (%rax,%rdx), %ymm1, %ymm0
    vpcmpeqb 32(%rax,%rdx), %ymm1, %ymm2
    vpmovmskb %ymm0, %r9d
    vpmovmskb %ymm2, %r11d
    popcntl %r9d, %r9d
    popcntl %r11d, %r11d
    addq %r9, %r10
    addq %r11, %r10
    vpcmpeqb 64(%rax,%rdx), %ymm1, %ymm0
    vpcmpeqb 96(%rax,%rdx), %ymm1, %ymm2
    vpmovmskb %ymm0, %r9d
    vpmovmskb %ymm2, %r11d
    popcntl %r9d, %r9d
    popcntl %r11d, %r11d
    addq %r9, %r10
    addq %r11, %r10
    addq $128, %rdx
    jmp .L6
.L4:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $32, %r9
    jl .L5
    vmovdqu (%rax,%rdx), %ymm0
    vpcmpeqb %ymm1, %ymm0, %ymm0
    vpmovmskb %ymm0, %r9d
    popcntl %r9d, %r9d
    addq %r9, %r10
    addq $32, %rdx
    jmp .L4
.L5:
    vzeroupper
.L0:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $16, %r9
    jl .L1
    movdqu (%rax,%rdx), %xmm0
    pcmpeqb %xmm1, %xmm0
    pmovmskb %xmm0, %r9d
    popcntl %r9d, %r9d
    addq %r9, %r10
    addq $16, %rdx
    jmp .L0
.L1:
    cmpq %r8, %rdx
    jge .L3
    movzbq (%rax,%rdx), %r9
    cmpq %rcx, %r9
    jne .L2
    addq $1, %r10
.L2:
    addq $1, %rdx
    jmp .L1
.L3:
    movq %r10, %rax
    leaq (%r12,%rax), %r14
    movl $98, %esi
    movq %rsi, %rcx
    movq %rbx, %rax
    movq 8(%rax), %r8
    movq (%rax), %rax
    xorq %rdx, %rdx
    xorq %r10, %r10
    cmpq $255, %rcx
    ja .L12
    movd %ecx, %xmm1
    punpcklbw %xmm1, %xmm1
    punpcklwd %xmm1, %xmm1
    pshufd $0, %xmm1, %xmm1
    vpbroadcastb %xmm1, %ymm1
.L15:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $128, %r9
    jl .L13
    cmpq $1024, %r9
    jl .L16
    leaq 1024(%rdx), %r11
    vpxor %ymm2, %ymm2, %ymm2
.L17:
    vpcmpeqb (%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 32(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 64(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    vpcmpeqb 96(%rax,%rdx), %ymm1, %ymm0
    vpsubb %ymm0, %ymm2, %ymm2
    addq $128, %rdx
    cmpq %r11, %rdx
    jl .L17
    vpxor %ymm3, %ymm3, %ymm3
    vpsadbw %ymm3, %ymm2, %ymm2
    subq $32, %rsp
    vmovdqu %ymm2, (%rsp)
    addq (%rsp), %r10
    addq 8(%rsp), %r10
    addq 16(%rsp), %r10
    addq 24(%rsp), %r10
    addq $32, %rsp
    jmp .L15
.L16:
    vpcmpeqb (%rax,%rdx), %ymm1, %ymm0
    vpcmpeqb 32(%rax,%rdx), %ymm1, %ymm2
    vpmovmskb %ymm0, %r9d
    vpmovmskb %ymm2, %r11d
    popcntl %r9d, %r9d
    popcntl %r11d, %r11d
    addq %r9, %r10
    addq %r11, %r10
    vpcmpeqb 64(%rax,%rdx), %ymm1, %ymm0
    vpcmpeqb 96(%rax,%rdx), %ymm1, %ymm2
    vpmovmskb %ymm0, %r9d
    vpmovmskb %ymm2, %r11d
    popcntl %r9d, %r9d
    popcntl %r11d, %r11d
    addq %r9, %r10
    addq %r11, %r10
    addq $128, %rdx
    jmp .L15
.L13:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $32, %r9
    jl .L14
    vmovdqu (%rax,%rdx), %ymm0
    vpcmpeqb %ymm1, %ymm0, %ymm0
    vpmovmskb %ymm0, %r9d
    popcntl %r9d, %r9d
    addq %r9, %r10
    addq $32, %rdx
    jmp .L13
.L14:
    vzeroupper
.L9:
    movq %r8, %r9
    subq %rdx, %r9
    cmpq $16, %r9
    jl .L10
    movdqu (%rax,%rdx), %xmm0
    pcmpeqb %xmm1, %xmm0
    pmovmskb %xmm0, %r9d
    popcntl %r9d, %r9d
    addq %r9, %r10
    addq $16, %rdx
    jmp .L9
.L10:
    cmpq %r8, %rdx
    jge .L12
    movzbq (%rax,%rdx), %r9
    cmpq %rcx, %r9
    jne .L11
    addq $1, %r10
.L11:
    addq $1, %rdx
    jmp .L10
.L12:
    movq %r10, %rax
    addq %rax, %r14
    movslq %r14d, %r12
    addq $1, %r13
    cmpq $6000, %r13
    jge .Lssa_main_21
    jmp .Lssa_main_19
.Lssa_main_1:
    movq %r12, %rax
    movslq %eax, %rcx
    movl %ecx, %edx
    shrl $31, %edx
    movl $-1461174440, %eax
    imulq %rcx, %rax
    sarq $38, %rax
    addl %edx, %eax
    imull $97, %eax, %eax
    subl %eax, %ecx
    movl %ecx, %eax
    cltq
    .cfi_remember_state
    leaq 8(%rsp), %rsp
    .cfi_def_cfa_offset 40
    popq %r14
    .cfi_def_cfa_offset 32
    popq %r13
    .cfi_def_cfa_offset 24
    popq %r12
    .cfi_def_cfa_offset 16
    popq %rbx
    .cfi_def_cfa_offset 8
    ret
    .cfi_restore_state
    .cfi_endproc
_start:
    xorl %edi, %edi
    movq $1073741824, %rsi
    movl $3, %edx
    movl $0x24022, %r10d
    movq $-1, %r8
    xorl %r9d, %r9d
    movl $9, %eax
    syscall
    cmpq $-4096, %rax
    ja .Lfern_stack_map_failed
    movq %rax, %rbx
    movq %rax, %rdi
    movq $1048576, %rsi
    xorl %edx, %edx
    movl $10, %eax
    syscall
    testq %rax, %rax
    jnz .Lfern_stack_guard_failed
    leaq 1073741808(%rbx), %rsp
    movq %rsp, __fern_stack_top(%rip)
    xorl %edi, %edi
    movq $65536, %rsi
    movl $3, %edx
    movl $0x24022, %r10d
    movq $-1, %r8
    xorl %r9d, %r9d
    movl $9, %eax
    syscall
    cmpq $-4096, %rax
    ja .Lfern_signal_stack_map_failed
    subq $32, %rsp
    movq %rax, (%rsp)
    movq $0, 8(%rsp)
    movq $65536, 16(%rsp)
    movq %rsp, %rdi
    xorl %esi, %esi
    movl $131, %eax
    syscall
    testq %rax, %rax
    jnz .Lfern_signal_stack_failed
    leaq __fern_segv(%rip), %rax
    movq %rax, (%rsp)
    movq $0x0c000004, 8(%rsp)
    call .Lfern_fault_install
    testq %rax, %rax
    jnz .Lfern_fault_handler_failed
    addq $32, %rsp
    pushq %rbp
    movq %rsp, %rbp
    call __fn_main
    movq %rax, %rdi
    movq $231, %rax
    syscall
    .section .rodata
.S0: .ascii "abcdefghijklmnopqrstuvwxyz012345"
    .section .data.rel.ro
    .balign 8
    .quad -1
.SB0:
    .quad .S0
    .quad 32
    .section .bss
    .align 8
    .globl __fern_heap_ptr
__fern_heap_ptr: .quad 0
    .align 8
    .globl __fern_heap_end
__fern_heap_end: .quad 0
    .align 8
    .globl __fern_heap_base
__fern_heap_base: .quad 0
    .align 8
__fern_rc_underflow: .quad 0
    .align 8
__fern_map_seed: .quad 0
    .align 8
    .globl __fern_arr_push_shared
__fern_arr_push_shared: .quad 0
    .align 8
    .globl __fern_arr_push_copied
__fern_arr_push_copied: .quad 0
    .align 8
__fern_freelist: .skip 524288
    .align 8
__fern_large_freelist: .skip 20480
    .align 8
__fern_freelist_shadow: .skip 524288
    .align 8
__fern_large_freelist_shadow: .skip 20480
    .section .text
__fern_alloc:
    addq $7, %rdi
    andq $-8, %rdi
    cmpq $2048, %rdi
    ja .Lalloc_full
    leaq __fern_freelist(%rip), %rax
    addq %rax, %rdi
    movq (%rdi), %rax
    testq %rax, %rax
    jz .Lalloc_fast_miss
    pushq (%rax)
    popq (%rdi)
    ret
.Lalloc_fast_miss:
    leaq __fern_freelist(%rip), %rax
    subq %rax, %rdi
    movq __fern_heap_ptr(%rip), %rax
    testq %rax, %rax
    jz .Lalloc_full
    addq %rax, %rdi
    cmpq __fern_heap_end(%rip), %rdi
    ja .Lalloc_oom
    movq %rdi, __fern_heap_ptr(%rip)
    ret
.Lalloc_full:
    pushq %rbp
    movq %rsp, %rbp
    pushq %rcx
    pushq %rdx
    movq %rdi, %rcx
    shrq $3, %rcx
    cmpq $65536, %rcx
    jae .Lalloc_large
    cmpq $256, %rcx
    jbe .L18
    pushq %rcx
    call __fern_capw
    popq %rcx
.L18:
    leaq 0(,%rcx,8), %rdi
    leaq __fern_freelist(%rip), %rdx
    movq (%rdx,%rcx,8), %rax
    testq %rax, %rax
    jz .Lalloc_nofl
    movq (%rax), %rdi
    movq %rdi, (%rdx,%rcx,8)
    popq %rdx
    popq %rcx
    popq %rbp
    ret
.Lalloc_large:
    movabs $17179869184, %rax
    cmpq %rax, %rdi
    ja .Lalloc_nofl
    movq %rdi, %rax
    cmpq $524288, %rax
    jae .L19
    movl $524288, %eax
.L19:
    bsrq %rax, %rcx
    subl $2, %ecx
    movl $1, %edx
    shlq %cl, %rdx
    leaq -1(%rax,%rdx), %rax
    negq %rdx
    andq %rdx, %rax
    bsrq %rax, %rcx
    subl $2, %ecx
    movq %rax, %rdx
    shrq %cl, %rdx
    shlq $2, %rcx
    addq %rcx, %rdx
    subq $72, %rdx
    movq %rax, %rdi
    movq %rdx, %rax
    leaq __fern_large_freelist(%rip), %rdx
    movq (%rdx,%rax,8), %rcx
    testq %rcx, %rcx
    jz .Lalloc_nofl
    movq (%rcx), %rdi
    movq %rdi, (%rdx,%rax,8)
    movq %rcx, %rax
    popq %rdx
    popq %rcx
    popq %rbp
    ret
.Lalloc_nofl:
    popq %rdx
    popq %rcx
    movq __fern_heap_ptr(%rip), %rax
    testq %rax, %rax
    jne .Lalloc_inited
    pushq %rdi
    pushq %rsi
    pushq %rcx
    pushq %rdx
    pushq %r8
    pushq %r9
    pushq %r10
    pushq %r11
    movl $0x10000000, %edi
    movabs $17179869184, %rsi
    movl $3, %edx
    movl $0x4022, %r10d
    movq $-1, %r8
    xorl %r9d, %r9d
    movl $9, %eax
    syscall
    cmpq $0, %rax
    jl .Lalloc_map_fail
    movq %rax, __fern_heap_ptr(%rip)
    movq %rax, __fern_heap_base(%rip)
    movabs $17179869184, %rcx
    addq %rax, %rcx
    movq %rcx, __fern_heap_end(%rip)
    popq %r11
    popq %r10
    popq %r9
    popq %r8
    popq %rdx
    popq %rcx
    popq %rsi
    popq %rdi
.Lalloc_inited:
    movq __fern_heap_ptr(%rip), %rax
    addq %rax, %rdi
    jc __fern_alloc_size_abort
    cmpq __fern_heap_end(%rip), %rdi
    ja .Lalloc_oom
    movq %rdi, __fern_heap_ptr(%rip)
    popq %rbp
    ret
    .section .rodata
.Lmsg_oom:
    .ascii "fern: out of memory (heap arena exhausted)\n"
    .section .text
.Lalloc_oom:
    leaq .Lmsg_oom(%rip), %rsi
    movq $43, %rdx
    movq $125, %rdi
    jmp __fern_report
    .section .rodata
.Lmsg_map_fail:
    .ascii "fern: could not map the heap arena (errno "
    .section .text
.Lalloc_map_fail:
    negq %rax
    subq $96, %rsp
    leaq 96(%rsp), %rdi
    movl $10, %edx
    decq %rdi
    movb %dl, (%rdi)
    movl $41, %edx
    decq %rdi
    movb %dl, (%rdi)
    movl $10, %ecx
.Lmap_fail_digit:
    xorl %edx, %edx
    divq %rcx
    addl $48, %edx
    decq %rdi
    movb %dl, (%rdi)
    testq %rax, %rax
    jne .Lmap_fail_digit
    leaq .Lmsg_map_fail(%rip), %rsi
    addq $42, %rsi
    movl $42, %ecx
.Lmap_fail_copy:
    decq %rsi
    movb (%rsi), %dl
    decq %rdi
    movb %dl, (%rdi)
    decq %rcx
    jne .Lmap_fail_copy
    movq %rdi, %rsi
    leaq 96(%rsp), %rdx
    subq %rsi, %rdx
    movq $123, %rdi
    jmp __fern_report
    .section .rodata
.Lmsg_arr_oob:
    .ascii "fern: array index out of range\n"
    .section .text
__fern_oob_abort:
    leaq .Lmsg_arr_oob(%rip), %rsi
    movq $31, %rdx
    jmp __fern_abort_134
    .section .rodata
.Lmsg_str_oob:
    .ascii "fern: string index out of range\n"
    .section .text
__fern_str_oob_abort:
    leaq .Lmsg_str_oob(%rip), %rsi
    movq $32, %rdx
    jmp __fern_abort_134
    .section .rodata
.Lmsg_slice:
    .ascii "fern: slice range out of bounds\n"
    .section .text
__fern_slice_abort:
    leaq .Lmsg_slice(%rip), %rsi
    movq $32, %rdx
    jmp __fern_abort_134
    .section .rodata
.Lmsg_alloc_size:
    .ascii "fern: allocation size out of range\n"
    .section .text
__fern_alloc_size_abort:
    leaq .Lmsg_alloc_size(%rip), %rsi
    movq $35, %rdx
    jmp __fern_abort_134
    .section .rodata
.Lmsg_free_range:
    .ascii "fern: release of memory outside the heap\n"
    .section .text
.Lrawfree_bad:
    pushq (%rsp)
    pushq %rbp
    movq %rsp, %rbp
__fern_free_range_abort:
    leaq .Lmsg_free_range(%rip), %rsi
    movq $41, %rdx
    jmp __fern_abort_134
    .section .rodata
.Lmsg_double_free:
    .ascii "fern: release of memory that is already free\n"
    .section .text
.Lrawfree_double:
    pushq (%rsp)
    pushq %rbp
    movq %rsp, %rbp
__fern_double_free_abort:
    leaq .Lmsg_double_free(%rip), %rsi
    movq $45, %rdx
    jmp __fern_abort_134
__fern_arr_box:
    cmpq $268435455, %rdi
    ja __fern_alloc_size_abort.c
    pushq %rdi
    leaq 24(,%rdi,8), %rdi
    cmpq $2048, %rdi
    ja .Larrbox_alloc
    leaq __fern_freelist(%rip), %rax
    addq %rax, %rdi
    movq (%rdi), %rax
    testq %rax, %rax
    jz .Larrbox_miss
    pushq (%rax)
    popq (%rdi)
.Larrbox_hdr:
    popq %rdi
    movq %rdi, (%rax)
    movq $1, 8(%rax)
    movq %rdi, 16(%rax)
    addq $16, %rax
    ret
.Larrbox_miss:
    leaq __fern_freelist(%rip), %rax
    subq %rax, %rdi
.Larrbox_alloc:
    call __fern_alloc
    jmp .Larrbox_hdr
__fern_str_box:
    pushq %rsi
    pushq %rdi
    movq $24, %rdi
    call __fern_alloc
    movq $1, (%rax)
    popq %rdi
    popq %rsi
    movq %rdi, 8(%rax)
    movq %rsi, 16(%rax)
    addq $8, %rax
    ret
__fern_str_box_hdr:
    leaq -24(%rdi), %rax
    movl $1, (%rax)
    movl $1934589965, 4(%rax)
    movq %rdi, 8(%rax)
    movq %rsi, 16(%rax)
    addq $8, %rax
    ret
__fn___fern_rc_inc:
    movq 8(%rsp), %rax
    testb $1, %al
    jnz .Lshrcinc_ret
    cmpq $0x10000, %rax
    jb .Lshrcinc_ret
    andq $-3, %rax
    movl -8(%rax), %ecx
    testl %ecx, %ecx
    js .Lshrcinc_ret
    addl $1, %ecx
    movl %ecx, -8(%rax)
.Lshrcinc_ret:
    movq 8(%rsp), %rax
    ret
__fn___fern_rc_underflow_count:
    movl __fern_rc_underflow(%rip), %eax
    ret
__fern_large_push:
    movabs $17179869184, %rax
    cmpq %rax, %rsi
    ja .Llpush_ret
    movq %rsi, %rax
    cmpq $524288, %rax
    jae .L20
    movl $524288, %eax
.L20:
    bsrq %rax, %rcx
    subl $2, %ecx
    movl $1, %edx
    shlq %cl, %rdx
    leaq -1(%rax,%rdx), %rax
    negq %rdx
    andq %rdx, %rax
    bsrq %rax, %rcx
    subl $2, %ecx
    movq %rax, %rdx
    shrq %cl, %rdx
    shlq $2, %rcx
    addq %rcx, %rdx
    subq $72, %rdx
    movq %rdx, %rax
    leaq __fern_large_freelist(%rip), %rdx
    movq (%rdx,%rax,8), %rcx
    cmpq %r8, %rcx
    je .Lrawfree_double
    movq %rcx, (%r8)
    movq %r8, (%rdx,%rax,8)
.Llpush_ret:
    ret
__fern_capw:
    pushq %rax
    pushq %rcx
    pushq %rdx
    movq 32(%rsp), %rax
    cmpq $256, %rax
    jbe .Lcapw_ret
    bsrq %rax, %rcx
    subl $2, %ecx
    movl $1, %edx
    shlq %cl, %rdx
    leaq -1(%rax,%rdx), %rax
    negq %rdx
    andq %rdx, %rax
    cmpq $65536, %rax
    jb .Lcapw_store
    movl $65535, %eax
.Lcapw_store:
    movq %rax, 32(%rsp)
.Lcapw_ret:
    popq %rdx
    popq %rcx
    popq %rax
    ret
__fern_raw_free:
    testq %rdi, %rdi
    jz .Lrawfree_ret
    cmpq __fern_heap_base(%rip), %rdi
    jb .Lrawfree_bad
    cmpq __fern_heap_end(%rip), %rdi
    jae .Lrawfree_bad
    addq $7, %rsi
    andq $-8, %rsi
    cmpq $8, %rsi
    jb .Lrawfree_ret
    movq %rsi, %rcx
    shrq $3, %rcx
    cmpq $65536, %rcx
    jae .Lrawfree_large
    cmpq $256, %rcx
    jbe .L21
    pushq %rcx
    call __fern_capw
    popq %rcx
.L21:
    leaq __fern_freelist(%rip), %r8
    movq (%r8,%rcx,8), %rdx
    cmpq %rdi, %rdx
    je .Lrawfree_double
    movq %rdx, (%rdi)
    movq %rdi, (%r8,%rcx,8)
.Lrawfree_ret:
    ret
.Lrawfree_large:
    movq %rdi, %r8
    call __fern_large_push
    ret
__fn___fern_arr_dec:
    movq 8(%rsp), %rax
__fn___fern_arr_dec.r:
    cmpq $0x10000, %rax
    jb .Larrdec_ret
__fn___fern_arr_dec.u:
    movl -8(%rax), %ecx
    cmpl $1, %ecx
    jne .Larrdec_shared
    movl $0, -8(%rax)
    movl -16(%rax), %ecx
    addq $3, %rcx
    leaq -16(%rax), %rdx
    cmpq $256, %rcx
    ja .Larrdec_big
.Larrdec_push:
    leaq __fern_freelist(%rip), %r8
    movq (%r8,%rcx,8), %rdi
    movq %rdi, (%rdx)
    movq %rdx, (%r8,%rcx,8)
.Larrdec_ret:
    ret
.Larrdec_shared:
    testl %ecx, %ecx
    js .Larrdec_ret
    jg .Larrdec_dec
    incl __fern_rc_underflow(%rip)
    jmp .Larrdec_ret
.Larrdec_dec:
    subl $1, %ecx
    movl %ecx, -8(%rax)
    ret
.Larrdec_big:
    cmpq $65536, %rcx
    jae .Larrdec_large
    pushq %rcx
    call __fern_capw
    popq %rcx
    jmp .Larrdec_push
.Larrdec_large:
    leaq -16(%rax), %r8
    leaq 0(,%rcx,8), %rsi
    call __fern_large_push
    ret
__fn___fern_str_free:
    movq 8(%rsp), %rax
__fn___fern_str_free.r:
    testb $1, %al
    jnz .Lstrfree_ret
    cmpq $0x10000, %rax
    jb .Lstrfree_ret
__fn___fern_str_free.u:
    movl -8(%rax), %ecx
    cmpl $1, %ecx
    jne .Lstrfree_shared
    movl -4(%rax), %edx
    cmpl $1934589965, %edx
    je .Lstrfree_fused
    cmpl $1196576599, %edx
    je .Lstrfree_grown
    cmpl $1112494674, %edx
    je .Lstrfree_box
    movq (%rax), %rcx
    movq 8(%rax), %rdx
    movq __fern_heap_base(%rip), %rsi
    cmpq %rsi, %rcx
    jb .Lstrfree_box
    cmpq __fern_heap_end(%rip), %rcx
    jae .Lstrfree_box
    testq %rdx, %rdx
    jle .Lstrfree_box
    addq $7, %rdx
    shrq $3, %rdx
    cmpq $65536, %rdx
    jae .Lstrfree_large
    cmpq $256, %rdx
    jbe .L22
    pushq %rdx
    call __fern_capw
    popq %rdx
.L22:
    leaq __fern_freelist(%rip), %r8
    movq (%r8,%rdx,8), %rsi
    movq %rsi, (%rcx)
    movq %rcx, (%r8,%rdx,8)
.Lstrfree_box:
    movl $0, -8(%rax)
    leaq -8(%rax), %rdx
    leaq __fern_freelist(%rip), %r8
    movq 24(%r8), %rsi
    movq %rsi, (%rdx)
    movq %rdx, 24(%r8)
    jmp .Lstrfree_ret
.Lstrfree_large:
    movq %rcx, %r8
    leaq 0(,%rdx,8), %rsi
    pushq %rax
    call __fern_large_push
    popq %rax
    jmp .Lstrfree_box
.Lstrfree_grown:
    movq -16(%rax), %rdx
    leaq -16(%rax), %rcx
    jmp .Lstrfree_block
.Lstrfree_fused:
    movq 8(%rax), %rdx
    addq $7, %rdx
    shrq $3, %rdx
    addq $3, %rdx
    leaq -8(%rax), %rcx
.Lstrfree_block:
    movl $0, -8(%rax)
    cmpq $256, %rdx
    ja .Lstrfree_fused_big
.Lstrfree_fused_push:
    leaq __fern_freelist(%rip), %r8
    movq (%r8,%rdx,8), %rsi
    movq %rsi, (%rcx)
    movq %rcx, (%r8,%rdx,8)
    jmp .Lstrfree_ret
.Lstrfree_fused_big:
    cmpq $65536, %rdx
    jae .Lstrfree_fused_large
    pushq %rdx
    call __fern_capw
    popq %rdx
    jmp .Lstrfree_fused_push
.Lstrfree_fused_large:
    movq %rcx, %r8
    leaq 0(,%rdx,8), %rsi
    call __fern_large_push
    jmp .Lstrfree_ret
.Lstrfree_shared:
    testl %ecx, %ecx
    js .Lstrfree_ret
    je .Lstrfree_uf
    subl $1, %ecx
    movl %ecx, -8(%rax)
    ret
.Lstrfree_uf:
    incl __fern_rc_underflow(%rip)
.Lstrfree_ret:
    ret
__fn___fern_str_view_free:
    movq 8(%rsp), %rax
    testb $1, %al
    jnz .Lstrvfree_ret
    cmpq $0x10000, %rax
    jb .Lstrvfree_ret
    movl -8(%rax), %ecx
    testl %ecx, %ecx
    js .Lstrvfree_view
    jmp __fn___fern_str_free
.Lstrvfree_view:
    leaq -8(%rax), %rdx
    movq __fern_heap_base(%rip), %rsi
    cmpq %rsi, %rdx
    jb .Lstrvfree_ret
    cmpq __fern_heap_end(%rip), %rdx
    jae .Lstrvfree_ret
    movl $0, -8(%rax)
    leaq __fern_freelist(%rip), %r8
    movq 24(%r8), %rsi
    movq %rsi, (%rdx)
    movq %rdx, 24(%r8)
.Lstrvfree_ret:
    ret
__fn___fern_str_arr_free:
    .cfi_startproc
    movq 8(%rsp), %rax
__fn___fern_str_arr_free.r:
    pushq %rbx
    .cfi_def_cfa_offset 16
    .cfi_offset %rbx, -16
    pushq %r12
    .cfi_def_cfa_offset 24
    .cfi_offset %r12, -24
    pushq %r13
    .cfi_def_cfa_offset 32
    .cfi_offset %r13, -32
    movq %rax, %rbx
.Lssa___fern_str_arr_free_0:
    cmpq $65536, %rbx
    jb .Lssa___fern_str_arr_free_1
.Lssa___fern_str_arr_free_6:
    leaq -8(%rbx), %rsi
    movslq (%rsi), %rsi
    cmpq $1, %rsi
    jne .Lssa___fern_str_arr_free_1
.Lssa___fern_str_arr_free_5:
    movslq (%rbx), %r12
.Lssa___fern_str_arr_free_4:
    xorl %esi, %esi
.Lssa___fern_str_arr_free_15:
    cmpq %r12, %rsi
    jge .Lssa___fern_str_arr_free_1
.Lssa___fern_str_arr_free_16:
    leaq 1(%rsi), %r13
    movq (%rbx,%r13,8), %rsi
    testb $1, %sil
    jnz .Lssa___fern_str_arr_free_rcdeck36
    cmpq $0x10000, %rsi
    jb .Lssa___fern_str_arr_free_rcdeck36
    cmpl $1, -8(%rsi)
    jg .Lssa___fern_str_arr_free_rcdecd36
    cmpl $0, -8(%rsi)
    js .Lssa___fern_str_arr_free_rcdeck36
    movq %rsi, %rax
    call __fn___fern_str_free.u
    jmp .Lssa___fern_str_arr_free_rcdece36
.Lssa___fern_str_arr_free_rcdecd36:
    subl $1, -8(%rsi)
.Lssa___fern_str_arr_free_rcdeck36:
.Lssa___fern_str_arr_free_rcdece36:
    movq %r13, %rsi
    cmpq %r12, %rsi
    jl .Lssa___fern_str_arr_free_16
.Lssa___fern_str_arr_free_1:
    cmpq $0x10000, %rbx
    jb .Lssa___fern_str_arr_free_rcdeck40
    cmpl $1, -8(%rbx)
    jg .Lssa___fern_str_arr_free_rcdecd40
    cmpl $0, -8(%rbx)
    js .Lssa___fern_str_arr_free_rcdeck40
    movq %rbx, %rax
    call __fn___fern_arr_dec.u
    jmp .Lssa___fern_str_arr_free_rcdece40
.Lssa___fern_str_arr_free_rcdecd40:
    subl $1, -8(%rbx)
.Lssa___fern_str_arr_free_rcdeck40:
.Lssa___fern_str_arr_free_rcdece40:
    xorl %eax, %eax
    .cfi_remember_state
    popq %r13
    .cfi_def_cfa_offset 24
    popq %r12
    .cfi_def_cfa_offset 16
    popq %rbx
    .cfi_def_cfa_offset 8
    ret
    .cfi_restore_state
    .cfi_endproc
__fn___fern_rc_is_unique:
    movq 8(%rsp), %rcx
    movq $0, %rax
    testb $1, %cl
    jnz .Lshuniq_ret
    cmpq $0x10000, %rcx
    jb .Lshuniq_ret
    andq $-3, %rcx
    movl -8(%rcx), %edx
    testl %edx, %edx
    js .Lshuniq_ret
    cmpl $1, %edx
    jne .Lshuniq_ret
    movq $1, %rax
.Lshuniq_ret:
    ret
__fn___fern_str_grow:
    movq 16(%rsp), %r8
    movq 8(%rsp), %rdx
    movq 8(%rdx), %rsi
    movq (%rdx), %rdx
    jmp .Lsg_body
__fn___fern_str_grow_range:
    movq 32(%rsp), %r8
    movslq 16(%rsp), %rax
    movslq 8(%rsp), %rsi
    movq 24(%rsp), %rdx
    testq %rax, %rax
    js .Lsg_out
    cmpq 8(%rdx), %rsi
    jg .Lsg_out
    subq %rax, %rsi
    js .Lsg_out
    movq (%rdx), %rdx
    addq %rax, %rdx
    jmp .Lsg_body
.Lsg_out:
    xorl %eax, %eax
    ret
.Lsg_body:
    xorl %eax, %eax
    testb $1, %r8b
    jnz .Lsg_ret
    cmpq $0x10000, %r8
    jb .Lsg_ret
    movl -8(%r8), %ecx
    cmpl $1, %ecx
    jne .Lsg_ret
    movq 8(%r8), %rcx
    leaq (%rcx,%rsi), %rdi
    cmpq $0x7fffffff, %rdi
    ja .Lsg_ret
    cmpl $1196576599, -4(%r8)
    jne .Lsg_fused
    leaq 39(%rdi), %r10
    shrq $3, %r10
    cmpq -16(%r8), %r10
    ja .Lsg_ret
    jmp .Lsg_copy
.Lsg_fused:
    cmpl $1934589965, -4(%r8)
    jne .Lsg_ret
    leaq 31(%rcx), %r9
    shrq $3, %r9
    leaq 31(%rdi), %r10
    shrq $3, %r10
    cmpq %r9, %r10
    je .Lsg_copy
    cmpq $65536, %r9
    jae .Lsg_large
    pushq %r9
    call __fern_capw
    popq %r11
    cmpq %r11, %r10
    ja .Lsg_ret
    jmp .Lsg_fits
.Lsg_large:
    cmpq $0x8000000, %r10
    ja .Lsg_ret
    leaq 0(,%r9,8), %rax
    pushq %rcx
    pushq %rdx
    cmpq $524288, %rax
    jae .L23
    movl $524288, %eax
.L23:
    bsrq %rax, %rcx
    subl $2, %ecx
    movl $1, %edx
    shlq %cl, %rdx
    leaq -1(%rax,%rdx), %rax
    negq %rdx
    andq %rdx, %rax
    bsrq %rax, %rcx
    subl $2, %ecx
    movq %rax, %rdx
    shrq %cl, %rdx
    shlq $2, %rcx
    addq %rcx, %rdx
    subq $72, %rdx
    popq %rdx
    popq %rcx
    shrq $3, %rax
    cmpq %rax, %r10
    movl $0, %eax
    ja .Lsg_ret
.Lsg_fits:
.Lsg_copy:
    movq %rdi, 8(%r8)
    movq (%r8), %rdi
    addq %rcx, %rdi
    movq %rsi, %rcx
    movq %rdx, %rsi
    rep movsb
    movl $1, %eax
.Lsg_ret:
    ret
__fn___fern_str_grow_fresh:
    movq 16(%rsp), %r8
    movq 8(%rsp), %rdx
    movq 8(%rdx), %rsi
    movq (%rdx), %rdx
    jmp .Lsgf_body
__fn___fern_str_grow_fresh_range:
    movq 32(%rsp), %r8
    movslq 16(%rsp), %rax
    movslq 8(%rsp), %rsi
    movq 24(%rsp), %rdx
    testq %rax, %rax
    js __fern_slice_abort.c
    cmpq 8(%rdx), %rsi
    jg __fern_slice_abort.c
    subq %rax, %rsi
    js __fern_slice_abort.c
    movq (%rdx), %rdx
    addq %rax, %rdx
.Lsgf_body:
    movq 8(%r8), %rcx
    leaq (%rcx,%rsi), %rdi
    cmpq $0x7fffffff, %rdi
    ja __fern_alloc_size_abort.c
    leaq 39(%rdi), %r9
    shrq $3, %r9
    leaq 31(%rcx), %r10
    shrq $3, %r10
    addq %r10, %r10
    cmpq %r9, %r10
    jae .Lsgf_class
    movq %r9, %r10
.Lsgf_class:
    cmpq $65536, %r10
    jae .Lsgf_large
    pushq %r10
    call __fern_capw
    popq %r10
    jmp .Lsgf_alloc
.Lsgf_large:
    cmpq $0x8000000, %r10
    ja .Lsgf_alloc
    leaq 0(,%r10,8), %rax
    pushq %rcx
    pushq %rdx
    cmpq $524288, %rax
    jae .L24
    movl $524288, %eax
.L24:
    bsrq %rax, %rcx
    subl $2, %ecx
    movl $1, %edx
    shlq %cl, %rdx
    leaq -1(%rax,%rdx), %rax
    negq %rdx
    andq %rdx, %rax
    bsrq %rax, %rcx
    subl $2, %ecx
    movq %rax, %rdx
    shrq %cl, %rdx
    shlq $2, %rcx
    addq %rcx, %rdx
    subq $72, %rdx
    popq %rdx
    popq %rcx
    shrq $3, %rax
    movq %rax, %r10
.Lsgf_alloc:
    leaq 0(,%r10,8), %rdi
    call __fern_alloc
    movq %r10, (%rax)
    movl $1, 8(%rax)
    movl $1196576599, 12(%rax)
    leaq 32(%rax), %rdi
    movq %rdi, 16(%rax)
    leaq (%rcx,%rsi), %r9
    movq %r9, 24(%rax)
    movq %rsi, %r9
    movq (%r8), %rsi
    rep movsb
    movq %rdx, %rsi
    movq %r9, %rcx
    rep movsb
    addq $16, %rax
    ret
__fn___fern_alloc_reuse:
    movq 16(%rsp), %rax
    movq 8(%rsp), %rsi
__fn___fern_alloc_reuse.r:
    testq %rax, %rax
    jz .Lsarelo_fresh
    cmpq %rsi, -16(%rax)
    je .Lsarelo_ret
    pushq %rsi
    call __fn___fern_arr_dec.r
    popq %rsi
.Lsarelo_fresh:
    movq %rsi, %rdi
    jmp __fern_arr_box
.Lsarelo_ret:
    ret
    .text
    .section .bss
    .align 8
__fern_sig_sp: .quad 0
    .section .rodata
.Lmsg_stack_overflow:
    .ascii "fern: stack overflow\n"
.Lmsg_illegal_instruction:
    .ascii "fern: illegal instruction\n"
.Lmsg_bus_error:
    .ascii "fern: bus error\n"
    .text
__fern_segv:
    cmpq $11, %rdi
    jne .Lfern_fatal_signal
    movq 16(%rsi), %rax
    movq __fern_stack_top(%rip), %rcx
    subq $1073741808, %rcx
    subq %rcx, %rax
    cmpq $1048576, %rax
    jae .Lfern_segv_other
.Lfern_segv_overflow:
    movq 168(%rdx), %rax
    movq 120(%rdx), %rcx
    subq $16, %rsp
    movq %rcx, (%rsp)
    movq %rax, 8(%rsp)
    movq %rsp, %rbp
    movq 160(%rdx), %rax
    movq %rax, __fern_bt_floor(%rip)
    leaq .Lmsg_stack_overflow(%rip), %rsi
    movq $21, %rdx
    movq $134, %rdi
    jmp __fern_report
.Lfern_segv_other:
    subq $32, %rsp
    movq $0, (%rsp)
    movq $0x04000000, 8(%rsp)
    call .Lfern_fault_install
    addq $32, %rsp
    ret
.Lfern_segv_restorer:
    movl $15, %eax
    syscall
.Lfern_fault_install:
    leaq .Lfern_segv_restorer(%rip), %rax
    movq %rax, 24(%rsp)
    movq $0x448, 32(%rsp)
    leaq 8(%rsp), %rsi
    xorl %edx, %edx
    movl $8, %r10d
    movl $11, %edi
    movl $13, %eax
    syscall
    testq %rax, %rax
    jnz .Lfern_fault_install_done
    movl $4, %edi
    movl $13, %eax
    syscall
    testq %rax, %rax
    jnz .Lfern_fault_install_done
    movl $7, %edi
    movl $13, %eax
    syscall
    testq %rax, %rax
    jnz .Lfern_fault_install_done
.Lfern_fault_install_done:
    ret
.Lfern_fatal_signal:
    movslq 8(%rsi), %rax
    testq %rax, %rax
    jle .Lfern_signal_sent
    movq %rsp, __fern_sig_sp(%rip)
    movq 168(%rdx), %rax
    movq 120(%rdx), %rcx
    subq $16, %rsp
    movq %rcx, (%rsp)
    movq %rax, 8(%rsp)
    movq %rsp, %rbp
    movq 160(%rdx), %rax
    movq %rax, __fern_bt_floor(%rip)
    leaq .Lmsg_illegal_instruction(%rip), %rsi
    movq $26, %rdx
    cmpq $4, %rdi
    je .Lfern_fatal_report
    leaq .Lmsg_bus_error(%rip), %rsi
    movq $16, %rdx
.Lfern_fatal_report:
    movq $-1, %rdi
    jmp __fern_report
.Lfern_signal_resume:
    movq __fern_sig_sp(%rip), %rsp
    jmp .Lfern_segv_other
.Lfern_signal_sent:
    addq $128, %rdi
    movl $231, %eax
    syscall
    .section .rodata
.Lfern_stack_map_failed_msg:
    .ascii "fern: could not map the stack\n"
    .text
.Lfern_stack_map_failed:
    leaq .Lfern_stack_map_failed_msg(%rip), %rsi
    movq $30, %rdx
    movl $2, %edi
    movl $1, %eax
    syscall
    movl $134, %edi
    movl $231, %eax
    syscall
    .section .rodata
.Lfern_stack_guard_failed_msg:
    .ascii "fern: could not protect the stack guard\n"
    .text
.Lfern_stack_guard_failed:
    leaq .Lfern_stack_guard_failed_msg(%rip), %rsi
    movq $40, %rdx
    movl $2, %edi
    movl $1, %eax
    syscall
    movl $134, %edi
    movl $231, %eax
    syscall
    .section .rodata
.Lfern_signal_stack_map_failed_msg:
    .ascii "fern: could not map the signal stack\n"
    .text
.Lfern_signal_stack_map_failed:
    leaq .Lfern_signal_stack_map_failed_msg(%rip), %rsi
    movq $37, %rdx
    movl $2, %edi
    movl $1, %eax
    syscall
    movl $134, %edi
    movl $231, %eax
    syscall
    .section .rodata
.Lfern_signal_stack_failed_msg:
    .ascii "fern: could not install the signal stack\n"
    .text
.Lfern_signal_stack_failed:
    leaq .Lfern_signal_stack_failed_msg(%rip), %rsi
    movq $41, %rdx
    movl $2, %edi
    movl $1, %eax
    syscall
    movl $134, %edi
    movl $231, %eax
    syscall
    .section .rodata
.Lfern_fault_handler_failed_msg:
    .ascii "fern: could not install the fault handler\n"
    .text
.Lfern_fault_handler_failed:
    leaq .Lfern_fault_handler_failed_msg(%rip), %rsi
    movq $42, %rdx
    movl $2, %edi
    movl $1, %eax
    syscall
    movl $134, %edi
    movl $231, %eax
    syscall
    .section .rodata
__fern_build:
    .ascii "FERNBUILD1 target=x86-64-linux profile= harden=0 pie=0 sanitize=0 backtrace=1 size=0 shared=0 heap_limit=17179869184 quarantine=67108864\n"
    .text
    .section .bss
    .align 8
    .globl __fern_stack_top
__fern_stack_top: .quad 0
    .globl __fern_bt_floor
__fern_bt_floor: .quad 0
    .section .text
__fern_abort_134:
    movq $134, %rdi
__fern_report:
    movq %rdi, %r15
    movq $2, %rdi
    movq $1, %rax
    syscall
    jmp __fern_bt_exit
    .section .rodata
.Lmsg_bt:
    .ascii "backtrace:\n"
    .section .text
    .globl __fern_bt_exit
__fern_bt_exit:
    andq $-16, %rsp
    movq %rsp, %r13
    leaq .Lmsg_bt(%rip), %rsi
    movq $11, %rdx
    movq $2, %rdi
    movq $1, %rax
    syscall
    movq %rbp, %rbx
    movl $64, %r14d
.Lbt_loop:
    testl %r14d, %r14d
    jz .Lbt_done
    cmpq %r13, %rbx
    jb .Lbt_done
    testq $7, %rbx
    jnz .Lbt_done
    movq 8(%rbx), %rsi
    cmpq $0x10000, %rsi
    jb .Lbt_done
    call .Lbt_hex
    movq __fern_bt_floor(%rip), %r13
    movq $0, __fern_bt_floor(%rip)
    leaq 8(%rbx), %rax
    testq %r13, %r13
    cmovzq %rax, %r13
    movq (%rbx), %rbx
    decl %r14d
    movq __fern_stack_top(%rip), %rax
    testq %rax, %rax
    jz .Lbt_loop
    cmpq %rax, %rbx
    jae .Lbt_done
    jmp .Lbt_loop
.Lbt_done:
    testq %r15, %r15
    js .Lfern_signal_resume
    movq %r15, %rdi
    movq $231, %rax
    syscall
.Lbt_hex:
    subq $32, %rsp
    movl $0x78302020, (%rsp)
    movb $10, 20(%rsp)
    movl $16, %ecx
.Lbt_hloop:
    movq %rsi, %rax
    andl $15, %eax
    cmpb $10, %al
    jb .Lbt_hdec
    addb $87, %al
    jmp .Lbt_hput
.Lbt_hdec:
    addb $48, %al
.Lbt_hput:
    movb %al, 3(%rsp,%rcx)
    shrq $4, %rsi
    decl %ecx
    jnz .Lbt_hloop
    movq $1, %rax
    movq %rsp, %rsi
    movq $21, %rdx
    syscall
    addq $32, %rsp
    ret
    .text
__fern_slice_abort.c:
    pushq %rbp
    movq %rsp, %rbp
    jmp __fern_slice_abort
__fern_alloc_size_abort.c:
    pushq %rbp
    movq %rsp, %rbp
    jmp __fern_alloc_size_abort
