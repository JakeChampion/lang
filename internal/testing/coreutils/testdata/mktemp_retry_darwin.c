#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

static unsigned long long calls;
static unsigned long long stop;
static int random_fd = -1;
static int report_fd = -1;

__attribute__((constructor)) static void setup(void) {
  const char *s = getenv("FERN_PROBE_STOP");
  if (s) stop = strtoull(s, 0, 10);
  s = getenv("FERN_PROBE_REPORT");
  if (s) report_fd = open(s, O_WRONLY | O_TRUNC);
}
__attribute__((destructor)) static void finish(void) {
  if (report_fd >= 0) {
    dprintf(report_fd, "%llu\n", calls);
    close(report_fd);
  }
}
static int collision(void) {
  calls++;
  errno = stop && calls == stop ? EIO : EEXIST;
  return -1;
}
static int probe_open(const char *path, int flags, ...) {
  if (flags & O_CREAT) return collision();
  int fd = open(path, flags);
  if (!strcmp(path, "/dev/urandom") || !strcmp(path, "/dev/random")) random_fd = fd;
  return fd;
}
static int probe_mkdir(const char *path, mode_t mode) {
  (void)path; (void)mode;
  return collision();
}
static int probe_lstat(const char *path, struct stat *st) {
  (void)path; (void)st;
  return collision();
}
static ssize_t probe_read(int fd, void *buf, size_t size) {
  if (fd == random_fd && fd >= 0) {
    memset(buf, 0, size);
    return (ssize_t)size;
  }
  return read(fd, buf, size);
}
#define INTERPOSE(replacement, original) \
  __attribute__((used)) static struct { const void *r; const void *o; } \
    interpose_##original __attribute__((section("__DATA,__interpose"))) = \
      { (const void *)(uintptr_t)&replacement, (const void *)(uintptr_t)&original };
INTERPOSE(probe_open, open)
INTERPOSE(probe_mkdir, mkdir)
INTERPOSE(probe_lstat, lstat)
INTERPOSE(probe_read, read)
