/* JNI asset ranges and ownership, with real Linux descriptors. */
#define _GNU_SOURCE
#include <assert.h>
#include <dirent.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef union { void *l; int32_t i; int64_t j; } Value;
enum { CONTEXT, RESOLVER, URI, ASSET, PARCEL, CLASS, STRING, EXCEPTION, URI_CLASS, ASSET_CLASS };
enum { CLOSE=1, PARSE, RESOLVE, OPEN, START, LENGTH, GET_PARCEL, GET_FD };
static int objects[10], mode, pending, locals, frames, original = -1, closed;
static void *table[235], **env_table = table;
static const char *names[] = {"", "close", "parse", "getContentResolver", "openAssetFileDescriptor", "getStartOffset", "getDeclaredLength", "getParcelFileDescriptor", "getFd"};
static const char *sigs[] = {"", "()V", "(Ljava/lang/String;)Landroid/net/Uri;", "()Landroid/content/ContentResolver;", "(Landroid/net/Uri;Ljava/lang/String;)Landroid/content/res/AssetFileDescriptor;", "()J", "()J", "()Landroid/os/ParcelFileDescriptor;", "()I"};
static void *local(int kind) { locals++; assert(locals < 32); return objects + kind; }
static void *exception(void *e) { (void)e; return pending ? local(EXCEPTION) : 0; }
static void clear(void *e) { (void)e; pending = 0; }
static void delete_local(void *e, void *obj) { (void)e; assert(obj && locals > 0); locals--; }
static int push(void *e, int capacity) {
    (void)e; assert(!pending && !frames && !locals && capacity == 32);
    if (mode == 1) { pending = 1; return -1; }
    frames++; return 0;
}
static void *pop(void *e, void *result) {
    (void)e; assert(!pending && !result && frames == 1); locals = 0; frames = 0; return 0;
}
static void *find(void *e, const char *name) {
    (void)e; assert(!pending);
    if (mode == 3) { pending = 1; return 0; }
    if (!strcmp(name, "android/net/Uri")) return local(URI_CLASS);
    assert(!strcmp(name, "android/content/res/AssetFileDescriptor")); return local(ASSET_CLASS);
}
static void *get_class(void *e, void *obj) {
    (void)e; assert(obj && !pending);
    if (mode == 13 && obj == objects + ASSET) { pending = 1; return 0; }
    return local(CLASS);
}
static void *method(void *e, void *cls, const char *name, const char *sig) {
    (void)e; assert(cls && !pending);
    for (int i=1; i<=GET_FD; i++) if (!strcmp(name, names[i])) {
        assert(!strcmp(sig, sigs[i]));
        if ((mode == 4 && i == CLOSE) || (mode == 14 && i == GET_FD)) { pending = 1; return 0; }
        return (void *)(uintptr_t)i;
    }
    assert(0); return 0;
}
static void *new_string(void *e, const uint16_t *text, int count) {
    (void)e; assert(text && count > 0 && !pending);
    if (mode == 5) { pending = 1; return 0; }
    return local(STRING);
}
static void *parse(void *e, void *cls, void *id, Value *args) {
    (void)e; assert(cls == objects + URI_CLASS && (uintptr_t)id == PARSE && args[0].l && !pending);
    if (mode == 6) { pending = 1; return 0; }
    return mode == 7 ? 0 : local(URI);
}
static void create_file(void) {
    if (mode == 18) {
        int ends[2]; assert(pipe(ends) == 0); original = ends[0]; assert(close(ends[1]) == 0); return;
    }
    char name[] = "/tmp/fern-source-XXXXXX";
    original = mkstemp(name); assert(original >= 0); assert(unlink(name) == 0);
    char bytes[40]; for (int i=0; i<40; i++) bytes[i] = (char)i;
    assert(write(original, bytes, 40) == 40); assert(lseek(original, 3, SEEK_SET) == 3);
}
static void *object_call(void *e, void *obj, void *id, Value *args) {
    (void)e; assert(obj && id && !pending);
    switch ((uintptr_t)id) {
    case RESOLVE:
        assert(obj == objects + CONTEXT);
        if (mode == 8) { pending = 1; return 0; }
        return local(RESOLVER);
    case OPEN:
        assert(obj == objects + RESOLVER && args[0].l == objects + URI && args[1].l);
        if (mode == 9) { pending = 1; return 0; }
        if (mode == 10) return 0;
        create_file(); return local(ASSET);
    case GET_PARCEL: assert(obj == objects + ASSET); return local(PARCEL);
    default: assert(0); return 0;
    }
}
static int64_t long_call(void *e, void *obj, void *id, Value *args) {
    (void)e; (void)args; assert(obj == objects + ASSET && !pending);
    if ((uintptr_t)id == START) {
        if (mode == 11) { pending = 1; return 0; }
        return mode == 15 ? -1 : 8;
    }
    assert((uintptr_t)id == LENGTH);
    if (mode == 12) { pending = 1; return 0; }
    if (mode == 16) return -2;
    if (mode == 17) return 33;
    return mode == 21 ? -1 : 16;
}
static int32_t int_call(void *e, void *obj, void *id, Value *args) {
    (void)e; (void)args; assert(obj == objects + PARCEL && (uintptr_t)id == GET_FD && !pending);
    return mode == 19 ? -1 : original;
}
static void void_call(void *e, void *obj, void *id, Value *args) {
    (void)e; (void)args; assert(obj == objects + ASSET && (uintptr_t)id == CLOSE && !pending);
    assert(original >= 0); assert(close(original) == 0); original = -1; closed++;
    if (mode == 20) pending = 1;
}
int mock_source_descriptors(void) {
    DIR *dir = opendir("/proc/self/fd"); assert(dir); int count = 0;
    while (readdir(dir)) count++;
    assert(closedir(dir) == 0); return count;
}
void mock_source_mode(int value) {
    assert(!frames && !locals && original == -1); mode = value; pending = value == 2; closed = 0;
}
int mock_source_clean(void) { return !frames && !locals && original == -1 && pending == (mode == 2); }
int mock_source_closed(void) { return closed; }
int mock_source_verify(int fd, int64_t offset, int64_t length) {
    if (offset != 8 || length != (mode == 21 ? 32 : 16)) return 0;
    if (!(fcntl(fd, F_GETFD) & FD_CLOEXEC) || lseek(fd, 0, SEEK_CUR) != 3) return 0;
    char bytes[32]; if (pread(fd, bytes, (size_t)length, offset) != length) return 0;
    for (int i=0; i<length; i++) if (bytes[i] != i + 8) return 0;
    return 1;
}
void *mock_source_context(void) { return objects + CONTEXT; }
void *mock_source_env(void) {
    table[6] = find; table[15] = exception; table[17] = clear; table[19] = push; table[20] = pop;
    table[23] = delete_local; table[31] = get_class; table[33] = method; table[36] = object_call;
    table[51] = int_call; table[54] = long_call; table[63] = void_call; table[113] = method;
    table[116] = parse; table[163] = new_string;
    return &env_table;
}
