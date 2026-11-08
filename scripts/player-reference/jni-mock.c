/* A JNI lifecycle test double. It does not play media. */
#include <assert.h>
#include <stdint.h>
#include <string.h>

typedef struct { int kind; const char *text; } Object;
typedef union { void *l; int32_t i; int64_t j; } Value;
enum { ACTIVITY, LOADER, LISTENER_CLASS, LISTENER, PLAYER_CLASS, PLAYER, ATTRS_CLASS, BUILDER, ATTRS, TIMESTAMP, SURFACE, STRING, EXCEPTION, CLASS, URI_CLASS, URI };
static Object objects[16];
static Object exception = {EXCEPTION, 0}, text = {STRING, "mock"};
static void *table[235];
static void **env_table = table;
static uintptr_t context[8], activity[8], window[24];
static int mode, locals, globals, frames, saved_locals, pending, releases, opens, prepares, starts, global_calls;
static int64_t last_seek;

enum { CONSTRUCTOR=1, GET_LOADER, LOAD_CLASS, PREPARED, COMPLETED, ERROR, INFO, SEEKED,
       SET_USAGE, SET_CONTENT, BUILD, SET_ATTRS, RESET, SOURCE, PREPARE, SET_SURFACE,
       START, PAUSE, SEEK, POSITION, DURATION, PLAYING, WIDTH, HEIGHT, RELEASE,
       GET_TIMESTAMP, MEDIA_TIME, SYSTEM_TIME, PARSE };
static const char *names[] = {"", "<init>", "getClassLoader", "loadClass", "setOnPreparedListener", "setOnCompletionListener", "setOnErrorListener", "setOnInfoListener", "setOnSeekCompleteListener", "setUsage", "setContentType", "build", "setAudioAttributes", "reset", "setDataSource", "prepareAsync", "setSurface", "start", "pause", "seekTo", "getCurrentPosition", "getDuration", "isPlaying", "getVideoWidth", "getVideoHeight", "release", "getTimestamp", "getAnchorMediaTimeUs", "getAnchorSytemNanoTime", "parse"};
static const char *signatures[] = {"", "()V", "()Ljava/lang/ClassLoader;", "(Ljava/lang/String;)Ljava/lang/Class;", "(Landroid/media/MediaPlayer$OnPreparedListener;)V", "(Landroid/media/MediaPlayer$OnCompletionListener;)V", "(Landroid/media/MediaPlayer$OnErrorListener;)V", "(Landroid/media/MediaPlayer$OnInfoListener;)V", "(Landroid/media/MediaPlayer$OnSeekCompleteListener;)V", "(I)Landroid/media/AudioAttributes$Builder;", "(I)Landroid/media/AudioAttributes$Builder;", "()Landroid/media/AudioAttributes;", "(Landroid/media/AudioAttributes;)V", "()V", "(Landroid/content/Context;Landroid/net/Uri;)V", "()V", "(Landroid/view/Surface;)V", "()V", "()V", "(JI)V", "()I", "()I", "()Z", "()I", "()I", "()V", "()Landroid/media/MediaTimestamp;", "()J", "()J", "(Ljava/lang/String;)Landroid/net/Uri;"};

static Object *local(Object *obj) { assert(obj); locals++; assert(locals < 64); return obj; }
static Object *find_class(void *env, const char *name) {
    (void)env;
    if (!strcmp(name, "android/media/MediaPlayer")) return local(objects + PLAYER_CLASS);
    if (!strcmp(name, "android/net/Uri")) {
        if (mode == 22) { pending = 1; return 0; }
        return local(objects + URI_CLASS);
    }
    assert(!strcmp(name, "android/media/AudioAttributes$Builder")); return local(objects + ATTRS_CLASS);
}
static Object *get_class(void *env, Object *obj) {
    (void)env; assert(obj);
    if (mode == 17) { pending = 1; return 0; }
    return local(objects + CLASS);
}
static void *method_id(void *env, Object *cls, const char *name, const char *sig) {
    (void)env; assert(cls && !pending);
    for (int i=1; i<=PARSE; i++) if (!strcmp(name, names[i])) {
        assert(!strcmp(sig, signatures[i]));
        if (mode == 11 && i == START) { pending = 1; return 0; }
        return (void *)(uintptr_t)i;
    }
    assert(0); return 0;
}
static void *static_method_id(void *env, Object *cls, const char *name, const char *sig) {
    assert(cls == objects + URI_CLASS && !strcmp(name, "parse") && !strcmp(sig, "(Ljava/lang/String;)Landroid/net/Uri;"));
    if (mode == 23) { pending = 1; return 0; }
    return method_id(env, cls, name, sig);
}
static Object *static_object_call(void *env, Object *cls, void *method, Value *args) {
    (void)env; assert(cls == objects + URI_CLASS && (uintptr_t)method == PARSE && args[0].l && !pending);
    if (mode == 24) { pending = 1; return 0; }
    return mode == 25 ? 0 : local(objects + URI);
}
static Object *new_object(void *env, Object *cls, void *method, Value *args) {
    (void)env; (void)args; assert(cls && (uintptr_t)method == CONSTRUCTOR && !pending);
    if (cls == objects + LISTENER_CLASS) return local(objects + LISTENER);
    if (cls == objects + PLAYER_CLASS) {
        if (mode == 2) { pending = 1; return 0; }
        return local(objects + PLAYER);
    }
    assert(cls == objects + ATTRS_CLASS); return local(objects + BUILDER);
}
static Object *object_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; assert(obj && method && !pending);
    switch ((uintptr_t)method) {
    case GET_LOADER: assert(obj == objects + ACTIVITY); return local(objects + LOADER);
    case LOAD_CLASS:
        assert(obj == objects + LOADER && args[0].l);
        if (mode == 1) { pending = 1; return 0; }
        return local(objects + LISTENER_CLASS);
    case SET_USAGE: assert(obj == objects + BUILDER && args[0].i == 1); return local(obj);
    case SET_CONTENT: assert(obj == objects + BUILDER && args[0].i == 3); return local(obj);
    case BUILD:
        assert(obj == objects + BUILDER);
        if (mode == 4) { pending = 1; return 0; }
        return local(objects + ATTRS);
    case GET_TIMESTAMP: assert(obj == objects + PLAYER); return mode == 13 ? 0 : local(objects + TIMESTAMP);
    default: assert(0); return 0;
    }
}
static void void_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; assert(obj == objects + PLAYER && method && !pending);
    switch ((uintptr_t)method) {
    case PREPARED: case COMPLETED: case ERROR: case INFO: case SEEKED:
        assert(args[0].l == objects + LISTENER);
        if (mode == 3 && (uintptr_t)method == INFO) pending = 1;
        break;
    case SET_ATTRS: assert(args[0].l == objects + ATTRS); if (mode == 5) pending = 1; break;
    case RESET: if (mode == 12) pending = 1; break;
    case SOURCE:
        assert(args[0].l == objects + ACTIVITY && args[1].l == objects + URI);
        opens++; if (mode == 26) pending = 1;
        break;
    case PREPARE: prepares++; break;
    case SET_SURFACE: assert(!args[0].l || args[0].l == objects + SURFACE); break;
    case START: starts++; break;
    case PAUSE: break;
    case SEEK: last_seek = args[0].j; assert(args[1].i == 3); break;
    case RELEASE: releases++; if (mode == 16) pending = 1; break;
    default: assert(0);
    }
}
static int32_t int_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(obj == objects + PLAYER && method && !pending);
    switch ((uintptr_t)method) {
    case POSITION: return 1234;
    case DURATION: if (mode == 18) { pending = 1; return 0; } return 4000;
    case WIDTH: return 320;
    case HEIGHT: return 240;
    default: assert(0); return 0;
    }
}
static uint8_t boolean_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(obj == objects + PLAYER && (uintptr_t)method == PLAYING && !pending); return 1;
}
static int64_t long_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(obj == objects + TIMESTAMP && method && !pending);
    if (mode == 14) { pending = 1; return 0; }
    if (mode == 15) return -1;
    return (uintptr_t)method == MEDIA_TIME ? 1234000 : 9876543210LL;
}
static Object *new_string(void *env, const uint16_t *units, int32_t count) {
    (void)env; assert(units && count > 0 && !pending);
    if (mode == 10) { pending = 1; return 0; }
    return local(&text);
}
static Object *new_global(void *env, Object *obj) {
    (void)env; assert(obj && !pending); global_calls++;
    if ((mode == 6 && global_calls == 1) || (mode == 7 && global_calls == 2)) { pending = 1; return 0; }
    globals++; return obj;
}
static void delete_global(void *env, Object *obj) { (void)env; assert(obj && globals > 0); globals--; }
static void delete_local(void *env, Object *obj) { (void)env; assert(obj && locals > 0); locals--; }
static Object *exception_occurred(void *env) { (void)env; return pending ? local(&exception) : 0; }
static void exception_clear(void *env) { (void)env; pending = 0; }
static int32_t push(void *env, int32_t capacity) {
    (void)env; assert(!pending && frames == 0 && capacity == 64);
    if (mode == 8) { pending = 1; return -1; }
    saved_locals = locals; frames++; return 0;
}
static Object *pop(void *env, Object *result) {
    (void)env; assert(!pending && !result && frames == 1); locals = saved_locals; frames--; return 0;
}

void mock_player_mode(int value) { assert(frames == 0 && locals == 0); mode = value; pending = value == 9; global_calls = 0; }
int mock_player_live(void) { return globals + locals + frames; }
int mock_player_pending(void) { return pending; }
int mock_player_releases(void) { return releases; }
int mock_player_opens(void) { return opens; }
int mock_player_prepares(void) { return prepares; }
int64_t mock_player_seek(void) { return last_seek; }
void *mock_player_window(void) { return window; }
void *ANativeWindow_toSurface(void *env, void *native_window) {
    (void)env; assert(native_window == window && !pending);
    if (mode == 21) { pending = 1; return 0; }
    return local(objects + SURFACE);
}
void *mock_player_context(void) {
    for (int i=0; i<16; i++) objects[i].kind = i;
    context[2] = (uintptr_t)activity; context[4] = (uintptr_t)&env_table; activity[3] = (uintptr_t)(objects + ACTIVITY);
    table[6] = find_class; table[15] = exception_occurred; table[19] = push; table[20] = pop;
    table[21] = new_global; table[22] = delete_global; table[23] = delete_local; table[30] = new_object;
    table[31] = get_class; table[33] = method_id; table[36] = object_call; table[39] = boolean_call;
    table[51] = int_call; table[54] = long_call; table[63] = void_call; table[163] = new_string;
    table[17] = exception_clear; table[113] = static_method_id; table[116] = static_object_call;
    return context;
}
