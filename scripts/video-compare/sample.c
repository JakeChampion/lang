/* Fork after a small native exec so Python's resident memory cannot set the RSS floor. */
#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

int main(int argc, char **argv) {
    if (argc < 3) return 2;
    pid_t child = fork();
    if (child < 0) { perror("fork"); return 2; }
    if (child == 0) {
        execv(argv[2], argv+2);
        perror("execv"); _exit(127);
    }
    int status;
    struct rusage usage;
    pid_t result;
    do { result = wait4(child, &status, 0, &usage); } while (result < 0 && errno == EINTR);
    if (result != child) { perror("wait4"); return 2; }
    FILE *out = fopen(argv[1], "w");
    if (!out) { perror("resource report"); return 2; }
    fprintf(out, "{\"peak_rss_bytes\":%llu,\"process_user_ns\":%llu,\"process_system_ns\":%llu}\n",
            (unsigned long long)usage.ru_maxrss*1024,
            (unsigned long long)usage.ru_utime.tv_sec*1000000000 + (unsigned long long)usage.ru_utime.tv_usec*1000,
            (unsigned long long)usage.ru_stime.tv_sec*1000000000 + (unsigned long long)usage.ru_stime.tv_usec*1000);
    if (fclose(out)) { perror("resource report"); return 2; }
    return WIFEXITED(status) ? WEXITSTATUS(status) : WIFSIGNALED(status) ? 128+WTERMSIG(status) : 2;
}
