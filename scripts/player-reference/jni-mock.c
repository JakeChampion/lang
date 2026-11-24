/* A JNI lifecycle test double. It does not play media. */
#include <assert.h>
#include <stdint.h>
#include <string.h>

typedef struct { int kind; const char *text; } Object;
typedef union { void *l; int32_t i; int64_t j; float f; } Value;
enum { ACTIVITY, LOADER, LISTENER_CLASS, LISTENER, PLAYER_CLASS, PLAYER, ATTRS_CLASS, BUILDER, ATTRS, TIMESTAMP, SURFACE, STRING, EXCEPTION, CLASS, URI_CLASS, URI,
       TEXTURE_CLASS, TEXTURE, SURFACE_CLASS, TARGET_SURFACE, FLOATS, TRACKS, TRACK0, TRACK1, TRACK2,
       CONTEXT_CLASS, CLASS_LOADER_CLASS, CLASS_CLASS, ATTRS_VALUE_CLASS, PARAMS_CLASS, PARAMS, TIMESTAMP_CLASS, TRACK_CLASS,
       PREPARED_CLASS, COMPLETION_CLASS, ERROR_CLASS, INFO_CLASS, SEEK_CLASS, OBJECT_COUNT };
static const struct { int object; const char *name; } classes[] = {
    {CONTEXT_CLASS, "android/content/Context"}, {CLASS_LOADER_CLASS, "java/lang/ClassLoader"}, {CLASS_CLASS, "java/lang/Class"},
    {URI_CLASS, "android/net/Uri"}, {ATTRS_VALUE_CLASS, "android/media/AudioAttributes"},
    {ATTRS_CLASS, "android/media/AudioAttributes$Builder"}, {PARAMS_CLASS, "android/media/PlaybackParams"},
    {TIMESTAMP_CLASS, "android/media/MediaTimestamp"}, {TRACK_CLASS, "android/media/MediaPlayer$TrackInfo"},
    {PREPARED_CLASS, "android/media/MediaPlayer$OnPreparedListener"}, {COMPLETION_CLASS, "android/media/MediaPlayer$OnCompletionListener"},
    {ERROR_CLASS, "android/media/MediaPlayer$OnErrorListener"}, {INFO_CLASS, "android/media/MediaPlayer$OnInfoListener"},
    {SEEK_CLASS, "android/media/MediaPlayer$OnSeekCompleteListener"}, {TEXTURE_CLASS, "android/graphics/SurfaceTexture"},
    {SURFACE_CLASS, "android/view/Surface"}, {PLAYER_CLASS, "android/media/MediaPlayer"} };
#define CLASS_NAMES ((int)(sizeof(classes) / sizeof(*classes)))
static Object objects[OBJECT_COUNT];
static Object exception = {EXCEPTION, 0}, text = {STRING, "mock"};
static void *table[235];
static void **env_table = table;
static uintptr_t context[30], activity[8], window[24];
static int64_t senders[8];
static int mode, locals, globals, frames, saved_locals, pending, releases, opens, prepares, starts, global_calls, target_releases, nexts, tuned;
static int64_t last_seek;
static int last_seek_mode, audio_track, selected_track = -1;

enum { CONSTRUCTOR=1, GET_LOADER, LOAD_CLASS, PREPARED, COMPLETED, ERROR, INFO, SEEKED,
       SET_USAGE, SET_CONTENT, BUILD, SET_ATTRS, RESET, SOURCE, PREPARE, SET_SURFACE,
       START, PAUSE, SEEK, POSITION, DURATION, PLAYING, WIDTH, HEIGHT, RELEASE,
       GET_TIMESTAMP, MEDIA_TIME, SYSTEM_TIME, PARSE, TEXTURE_INIT, SURFACE_INIT, UPDATE, MATRIX, FRAME_TIME, SELECTED_TRACK, TRACK_INFO, TRACK_TYPE, SELECT_TRACK, SESSION, SET_NEXT,
       NEW_INSTANCE, SET_SPEED, SET_PARAMS, SET_VOLUME, TO_STRING };
static const char *names[] = {"", "<init>", "getClassLoader", "loadClass", "setOnPreparedListener", "setOnCompletionListener", "setOnErrorListener", "setOnInfoListener", "setOnSeekCompleteListener", "setUsage", "setContentType", "build", "setAudioAttributes", "reset", "setDataSource", "prepareAsync", "setSurface", "start", "pause", "seekTo", "getCurrentPosition", "getDuration", "isPlaying", "getVideoWidth", "getVideoHeight", "release", "getTimestamp", "getAnchorMediaTimeUs", "getAnchorSytemNanoTime", "parse", "<init>", "<init>", "updateTexImage", "getTransformMatrix", "getTimestamp", "getSelectedTrack", "getTrackInfo", "getTrackType", "selectTrack", "getAudioSessionId", "setNextMediaPlayer", "newInstance", "setSpeed", "setPlaybackParams", "setVolume", "toString"};
static const char *signatures[] = {"", "()V", "()Ljava/lang/ClassLoader;", "(Ljava/lang/String;)Ljava/lang/Class;", "(Landroid/media/MediaPlayer$OnPreparedListener;)V", "(Landroid/media/MediaPlayer$OnCompletionListener;)V", "(Landroid/media/MediaPlayer$OnErrorListener;)V", "(Landroid/media/MediaPlayer$OnInfoListener;)V", "(Landroid/media/MediaPlayer$OnSeekCompleteListener;)V", "(I)Landroid/media/AudioAttributes$Builder;", "(I)Landroid/media/AudioAttributes$Builder;", "()Landroid/media/AudioAttributes;", "(Landroid/media/AudioAttributes;)V", "()V", "(Landroid/content/Context;Landroid/net/Uri;)V", "()V", "(Landroid/view/Surface;)V", "()V", "()V", "(JI)V", "()I", "()I", "()Z", "()I", "()I", "()V", "()Landroid/media/MediaTimestamp;", "()J", "()J", "(Ljava/lang/String;)Landroid/net/Uri;", "(I)V", "(Landroid/graphics/SurfaceTexture;)V", "()V", "([F)V", "()J", "(I)I", "()[Landroid/media/MediaPlayer$TrackInfo;", "()I", "(I)V", "()I", "(Landroid/media/MediaPlayer;)V", "()Ljava/lang/Object;", "(F)Landroid/media/PlaybackParams;", "(Landroid/media/PlaybackParams;)V", "(FF)V", "()Ljava/lang/String;"};

static Object *local(Object *obj) { assert(obj); locals++; assert(locals < 64); return obj; }
static Object *find_class(void *env, const char *name) {
    (void)env; assert(frames == 1 && !pending);
    for (int i=0; i<CLASS_NAMES; i++) if (!strcmp(name, classes[i].name)) {
        if ((mode == 22 && classes[i].object == URI_CLASS) || (mode == 17 && classes[i].object == PLAYER_CLASS)) { pending = 1; return 0; }
        return local(objects + classes[i].object);
    }
    assert(0); return 0;
}
static int is_class(Object *obj) {
    for (int i=0; i<CLASS_NAMES; i++) if (obj == objects + classes[i].object) return 1;
    return 0;
}
/* Only the text of an exception is read through its class. */
static Object *get_class(void *env, Object *obj) { (void)env; assert(obj == &exception && !pending); return local(objects + CLASS); }
static void *method_id(void *env, Object *cls, const char *name, const char *sig) {
    (void)env; assert(cls && !pending);
    for (int i=1; i<=TO_STRING; i++) if (!strcmp(name, names[i]) && !strcmp(sig, signatures[i])) {
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
    (void)env; assert(cls && method && !pending);
    if (cls == objects + TEXTURE_CLASS) {
        assert((uintptr_t)method == TEXTURE_INIT && args[0].i == 7);
        if (mode == 27) { pending = 1; return 0; }
        return local(objects + TEXTURE);
    }
    if (cls == objects + SURFACE_CLASS) {
        assert((uintptr_t)method == SURFACE_INIT && args[0].l == objects + TEXTURE);
        if (mode == 28) { pending = 1; return 0; }
        return local(objects + TARGET_SURFACE);
    }
    assert((uintptr_t)method == CONSTRUCTOR);
    if (cls == objects + PARAMS_CLASS) return local(objects + PARAMS);
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
    case NEW_INSTANCE: assert(obj == objects + LISTENER_CLASS); return local(objects + LISTENER);
    case SET_SPEED: assert(obj == objects + PARAMS && args[0].f == 1.5f); return local(obj);
    case TO_STRING: assert(obj == &exception); return local(&text);
    case SET_USAGE: assert(obj == objects + BUILDER && args[0].i == 1); return local(obj);
    case SET_CONTENT: assert(obj == objects + BUILDER && args[0].i == 3); return local(obj);
    case BUILD:
        assert(obj == objects + BUILDER);
        if (mode == 4) { pending = 1; return 0; }
        return local(objects + ATTRS);
    case GET_TIMESTAMP: assert(obj == objects + PLAYER); return mode == 13 ? 0 : local(objects + TIMESTAMP);
    case TRACK_INFO: assert(obj == objects + PLAYER); return local(objects + TRACKS);
    default: assert(0); return 0;
    }
}
static void void_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; assert(obj && method && !pending);
    if (obj == objects + TEXTURE) {
        switch ((uintptr_t)method) {
        case UPDATE: return;
        case MATRIX: assert(args[0].l == objects + FLOATS); return;
        case RELEASE: target_releases += 10; return;
        default: assert(0);
        }
    }
    if (obj == objects + TARGET_SURFACE) { assert((uintptr_t)method == RELEASE); target_releases++; return; }
    assert(obj == objects + PLAYER);
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
    case SET_SURFACE: assert(!args[0].l || args[0].l == objects + SURFACE || args[0].l == objects + TARGET_SURFACE); break;
    case START: starts++; break;
    case PAUSE: break;
    case SELECT_TRACK: selected_track = args[0].i; break;
    case SET_NEXT: assert(!args[0].l || args[0].l == objects + PLAYER); nexts++; break;
    case SET_PARAMS: assert(args[0].l == objects + PARAMS); tuned++; break;
    case SET_VOLUME: assert(args[0].f == 0.5f && args[1].f == 0.5f); tuned++; break;
    case SEEK: last_seek = args[0].j; last_seek_mode = args[1].i; assert(last_seek_mode == 2 || last_seek_mode == 3); break;
    case RELEASE: releases++; if (mode == 16) pending = 1; break;
    default: assert(0);
    }
}
static int32_t int_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(method && !pending);
    if ((uintptr_t)method == TRACK_TYPE) {
        /* A video track, then two sound tracks. */
        assert(obj == objects + TRACK0 || obj == objects + TRACK1 || obj == objects + TRACK2);
        return obj == objects + TRACK0 ? 1 : 2;
    }
    assert(obj == objects + PLAYER);
    switch ((uintptr_t)method) {
    case POSITION: return 1234;
    case DURATION: if (mode == 18) { pending = 1; return 0; } return 4000;
    case WIDTH: return 320;
    case HEIGHT: return 240;
    case SESSION: return 77;
    case SELECTED_TRACK: assert(args[0].i == 2); return audio_track;
    default: assert(0); return 0;
    }
}
static uint8_t boolean_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(obj == objects + PLAYER && (uintptr_t)method == PLAYING && !pending); return 1;
}
static int64_t long_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; (void)args; assert(method && !pending);
    if (obj == objects + TEXTURE) { assert((uintptr_t)method == FRAME_TIME); return 5000; }
    assert(obj == objects + TIMESTAMP);
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
    (void)env; assert(obj && !pending);
    /* The failures are for the player and its listener, not for a class of the bindings. */
    if (is_class(obj)) { globals++; return obj; }
    global_calls++;
    if ((mode == 6 && global_calls == 1) || (mode == 7 && global_calls == 2)) { pending = 1; return 0; }
    globals++; return obj;
}
static void delete_global(void *env, Object *obj) { (void)env; assert(obj && globals > 0); globals--; }
static void delete_local(void *env, Object *obj) { (void)env; assert(obj && locals > 0); locals--; }
static Object *exception_occurred(void *env) { (void)env; return pending ? local(&exception) : 0; }
static void exception_clear(void *env) { (void)env; pending = 0; }
static int32_t utf_length(void *env, Object *obj) { (void)env; assert(obj == &text); return (int32_t)strlen(text.text); }
static const char *utf_chars(void *env, Object *obj, void *is_copy) { (void)env; (void)is_copy; assert(obj == &text); return text.text; }
static void release_chars(void *env, Object *obj, const char *chars) { (void)env; assert(obj == &text && chars == text.text); }
static Object *new_floats(void *env, int32_t count) { (void)env; assert(count == 16 && !pending); return local(objects + FLOATS); }
static void float_region(void *env, Object *array, int32_t start, int32_t count, float *out) {
    (void)env; assert(array == objects + FLOATS && start == 0 && count == 16 && !pending);
    for (int i=0; i<16; i++) out[i] = (float)i / 2;
}
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
int mock_player_seek_mode(void) { return last_seek_mode; }
void mock_player_audio_track(int track) { audio_track = track; }
int mock_player_target_releases(void) { return target_releases; }
int mock_player_selected_track(void) { return selected_track; }
int mock_player_nexts(void) { return nexts; }
int mock_player_tuned(void) { return tuned; }
static int32_t array_length(void *env, Object *array) { (void)env; assert(array == objects + TRACKS); return 3; }
static Object *array_element(void *env, Object *array, int32_t index) {
    (void)env; assert(array == objects + TRACKS && index >= 0 && index < 3);
    return local(objects + TRACK0 + index);
}
void *mock_player_window(void) { return window; }
void *ANativeWindow_toSurface(void *env, void *native_window) {
    (void)env; assert(native_window == window && !pending);
    if (mode == 21) { pending = 1; return 0; }
    return local(objects + SURFACE);
}
void *mock_player_context(void) {
    for (int i=0; i<OBJECT_COUNT; i++) objects[i].kind = i;
    context[2] = (uintptr_t)activity; context[4] = (uintptr_t)&env_table; context[29] = (uintptr_t)senders; activity[3] = (uintptr_t)(objects + ACTIVITY);
    table[6] = find_class; table[15] = exception_occurred; table[19] = push; table[20] = pop;
    table[21] = new_global; table[22] = delete_global; table[23] = delete_local; table[30] = new_object;
    table[31] = get_class; table[33] = method_id; table[36] = object_call; table[39] = boolean_call;
    table[51] = int_call; table[54] = long_call; table[63] = void_call; table[163] = new_string;
    table[17] = exception_clear; table[113] = static_method_id; table[116] = static_object_call;
    table[168] = utf_length; table[169] = utf_chars; table[170] = release_chars;
    table[181] = new_floats; table[205] = float_region;
    table[171] = array_length; table[173] = array_element;
    return context;
}
