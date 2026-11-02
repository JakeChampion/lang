.text
.global _start
_start:
    adrp x1, siginfo
    add x1, x1, :lo12:siginfo
    adrp x2, context
    add x2, x2, :lo12:context
    mov x13, xzr
    b __fern_segv
__fern_report:
    cmp x0, #134
    b.ne failure
    cmp x2, #21
    b.ne failure
    adrp x9, Lmsg_stack_overflow
    add x9, x9, :lo12:Lmsg_stack_overflow
    cmp x1, x9
    b.ne failure
    ldp x9, x10, [sp]
    mov x11, #0x3344
    cmp x9, x11
    b.ne failure
    mov x11, #0x1122
    cmp x10, x11
    b.ne failure
    mov x11, sp
    cmp x29, x11
    b.ne failure
    adrp x9, __fern_stack_top
    add x9, x9, :lo12:__fern_stack_top
    ldr x9, [x9]
    mov x10, #@STACK_TOP@
    cmp x9, x10
    b.ne failure
    adrp x9, __fern_bt_floor
    add x9, x9, :lo12:__fern_bt_floor
    ldr x9, [x9]
    movz x10, #0x1000, lsl #16
    movk x10, #0x20
    cmp x9, x10
    b.ne failure
    mov x0, #0
    b done
failure:
    mov x0, #1
done:
    mov x8, #93
    svc #0
__fern_segv_trampoline:
    b failure
@HANDLER@
.data
.balign 8
__fern_stack_top: .quad @STACK_TOP@
__fern_bt_floor: .quad 0
siginfo: .zero 24
    .quad 0x10000020
context: .zero 48
    .quad mcontext
mcontext: .zero 248
    .quad 0xffff000000003344
    .quad 0
    .quad 0x10000020
    .quad 0xffff000000001122
Lmsg_stack_overflow: .ascii "fern: stack overflow\n"
