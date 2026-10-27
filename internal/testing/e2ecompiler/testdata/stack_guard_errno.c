#include <errno.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

#if defined(__x86_64__)
#define HOST_AUDIT_ARCH AUDIT_ARCH_X86_64
#elif defined(__aarch64__)
#define HOST_AUDIT_ARCH AUDIT_ARCH_AARCH64
#else
#error Unsupported test host
#endif

/* Each mode makes one startup system call of a Fern program fail:
   guard            mprotect of the 1 MiB guard with PROT_NONE   -> EPERM
   signal-stack-map mmap of the 64 KiB alternate signal stack    -> ENOMEM
   signal-stack     sigaltstack                                   -> EPERM
   fault-handler    rt_sigaction for SIGSEGV                      -> EPERM
   The launcher verifies its own injection, then execs the program. */

#define ARCH_CHECK \
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, arch)), \
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, HOST_AUDIT_ARCH, 1, 0), \
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL_PROCESS), \
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr))

static struct sock_filter guard_rules[] = {
    ARCH_CHECK,
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_mprotect, 0, 7),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[1])),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 1048576, 0, 5),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[1]) + 4),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 0, 3),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[2])),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 0, 1),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
};

static struct sock_filter signal_stack_map_rules[] = {
    ARCH_CHECK,
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_mmap, 0, 5),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[1])),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 65536, 0, 3),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[1]) + 4),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 0, 0, 1),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | ENOMEM),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
};

static struct sock_filter signal_stack_rules[] = {
    ARCH_CHECK,
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_sigaltstack, 0, 1),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
};

static struct sock_filter fault_handler_rules[] = {
    ARCH_CHECK,
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_rt_sigaction, 0, 3),
    BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[0])),
    BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, 11, 0, 1),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
    BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
};

static int install(struct sock_filter *rules, unsigned short len) {
    struct sock_fprog program = {len, rules};
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) ||
        prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &program)) {
        perror("startup failure test seccomp");
        return 65;
    }
    return 0;
}

static int injected(long result, int want) {
    return result == -1 && errno == want;
}

int main(int argc, char **argv) {
    if (argc < 3) return 64;
    const char *mode = argv[1];
    int rc;
    errno = 0;
    if (strcmp(mode, "guard") == 0) {
        rc = install(guard_rules, sizeof(guard_rules) / sizeof(guard_rules[0]));
        if (rc) return rc;
        if (!injected(syscall(SYS_mprotect, 0, 1048576, 0), EPERM)) {
            fputs("startup failure test did not inject EPERM for mprotect\n", stderr);
            return 67;
        }
    } else if (strcmp(mode, "signal-stack-map") == 0) {
        rc = install(signal_stack_map_rules, sizeof(signal_stack_map_rules) / sizeof(signal_stack_map_rules[0]));
        if (rc) return rc;
        if (!injected(syscall(SYS_mmap, 0, 65536, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0), ENOMEM)) {
            fputs("startup failure test did not inject ENOMEM for mmap\n", stderr);
            return 67;
        }
    } else if (strcmp(mode, "signal-stack") == 0) {
        rc = install(signal_stack_rules, sizeof(signal_stack_rules) / sizeof(signal_stack_rules[0]));
        if (rc) return rc;
        if (!injected(syscall(SYS_sigaltstack, 0, 0), EPERM)) {
            fputs("startup failure test did not inject EPERM for sigaltstack\n", stderr);
            return 67;
        }
    } else if (strcmp(mode, "fault-handler") == 0) {
        rc = install(fault_handler_rules, sizeof(fault_handler_rules) / sizeof(fault_handler_rules[0]));
        if (rc) return rc;
        if (!injected(syscall(SYS_rt_sigaction, 11, 0, 0, 8), EPERM)) {
            fputs("startup failure test did not inject EPERM for rt_sigaction\n", stderr);
            return 67;
        }
    } else {
        fputs("startup failure test: unknown mode\n", stderr);
        return 64;
    }
    execv(argv[2], argv + 2);
    perror("startup failure test exec");
    return 66;
}
