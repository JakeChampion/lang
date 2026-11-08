/* A JNI table test double for MediaCodecList. It never decodes media. */
#include <assert.h>
#include <stdint.h>
#include <stddef.h>
#include <string.h>

typedef struct { int kind, id; const char *text; } Object;
typedef union { void *l; int32_t i; int64_t j; } Value;
enum { CLASS, LIST, FORMAT, INFO, CAPS, PROFILE, INFOS, TYPES, PROFILES, COLORS, STRING, EXCEPTION };
static Object classes[3] = {{CLASS,0,0},{CLASS,1,0},{CLASS,2,0}};
static Object list = {LIST,0,0}, format = {FORMAT,0,0}, exception = {EXCEPTION,0,0};
static Object infos_array = {INFOS,0,0}, infos[6], caps[6], types[6];
static Object profiles_array = {PROFILES,0,0}, profile = {PROFILE,0,0}, colors = {COLORS,0,0};
static const char *names[] = {"c2.vendor.avc.decoder", "c2.android.avc.decoder", "c2.vendor.avc.encoder", "c2.android.avc.encoder", "alias.avc.decoder", "other.hevc.decoder"};
static Object name_objects[6], avc = {STRING,0,"video/avc"}, hevc = {STRING,0,"video/hevc"};
static struct { Object obj; char bytes[513]; } strings[512];
static int string_count, frame_count, saved_strings[16], pending, mode, hardware_queries;
static void *table[235];
static void **env_table = table;

enum { CONSTRUCTOR=1, GET_INFOS, GET_TYPES, GET_NAME, GET_CAPABILITIES, IS_ENCODER, IS_SUPPORTED, IS_HARDWARE, IS_SOFTWARE, IS_VENDOR, IS_ALIAS, CREATE_FORMAT, SET_INTEGER };
static const char *method_names[] = {"", "<init>", "getCodecInfos", "getSupportedTypes", "getName", "getCapabilitiesForType", "isEncoder", "isFormatSupported", "isHardwareAccelerated", "isSoftwareOnly", "isVendor", "isAlias", "createVideoFormat", "setInteger"};
static const char *signatures[] = {"", "(I)V", "()[Landroid/media/MediaCodecInfo;", "()[Ljava/lang/String;", "()Ljava/lang/String;", "(Ljava/lang/String;)Landroid/media/MediaCodecInfo$CodecCapabilities;", "()Z", "(Landroid/media/MediaFormat;)Z", "()Z", "()Z", "()Z", "()Z", "(Ljava/lang/String;II)Landroid/media/MediaFormat;", "(Ljava/lang/String;I)V"};

void mock_jni_mode(int value) { assert(frame_count == 0); mode = value; pending = value == 6; hardware_queries = string_count = 0; }
int mock_jni_frames(void) { return frame_count; }
int mock_jni_pending(void) { return pending; }
int mock_jni_hardware_queries(void) { return hardware_queries; }
static Object *find_class(void *env, const char *name) {
    (void)env;
    if (!strcmp(name,"android/media/MediaCodecList")) return classes;
    assert(!strcmp(name,"android/media/MediaFormat")); return classes + 1;
}
static Object *get_class(void *env, Object *obj) { (void)env; assert(obj); return classes + 2; }
static void *method_id(void *env, Object *cls, const char *name, const char *sig) {
    (void)env; assert(cls && cls->kind == CLASS);
    for (int i=1; i<=SET_INTEGER; i++) if (!strcmp(name,method_names[i])) {
        assert(!strcmp(sig,signatures[i]));
        if (mode == 4 && i == GET_INFOS) { pending = 1; return NULL; }
        return (void *)(uintptr_t)i;
    }
    assert(0); return NULL;
}
static Object *new_object(void *env, Object *cls, void *method, Value *args) {
    (void)env; assert(cls == classes && (uintptr_t)method == CONSTRUCTOR && args[0].i == 0); return &list;
}
static Object *object_call(void *env, Object *obj, void *method, Value *args) {
    (void)env;
    switch ((uintptr_t)method) {
    case GET_INFOS: assert(obj == &list); return &infos_array;
    case GET_TYPES: assert(obj->kind == INFO); return types + obj->id;
    case GET_NAME: assert(obj->kind == INFO); return name_objects + obj->id;
    case GET_CAPABILITIES: assert(obj->kind == INFO && !strcmp(((Object *)args[0].l)->text,"video/avc")); return caps + obj->id;
    default: assert(0); return NULL;
    }
}
static uint8_t boolean_call(void *env, Object *obj, void *method, Value *args) {
    (void)env;
    switch ((uintptr_t)method) {
    case IS_ENCODER: assert(obj->kind == INFO); return obj->id == 2 || obj->id == 3;
    case IS_SUPPORTED:
        assert(obj->kind == CAPS && args[0].l == &format);
        if (mode == 2 && obj->id == 0) { pending = 1; return 0; }
        return !(mode == 1 && obj->id == 0);
    case IS_HARDWARE: hardware_queries++; return obj->id == 0 || obj->id == 2;
    case IS_SOFTWARE: hardware_queries++; return obj->id == 1 || obj->id == 3 || obj->id == 4;
    case IS_VENDOR: hardware_queries++; return obj->id == 0 || obj->id == 2;
    case IS_ALIAS: hardware_queries++; return obj->id == 4;
    default: assert(0); return 0;
    }
}
static Object *static_object_call(void *env, Object *cls, void *method, Value *args) {
    (void)env; assert(cls == classes + 1 && (uintptr_t)method == CREATE_FORMAT);
    assert(!strcmp(((Object *)args[0].l)->text,"video/avc") && args[1].i == 16 && args[2].i == 8); return &format;
}
static void void_call(void *env, Object *obj, void *method, Value *args) {
    (void)env; assert(obj == &format && (uintptr_t)method == SET_INTEGER);
    const char *key = ((Object *)args[0].l)->text;
    if (!strcmp(key,"frame-rate")) assert(args[1].i == 30);
    else if (!strcmp(key,"bitrate")) assert(args[1].i == 100000);
    else { assert(!strcmp(key,"color-format")); assert(args[1].i == 19); }
}
static void *field_id(void *env, Object *cls, const char *name, const char *sig) {
    (void)env; assert(cls && cls->kind == CLASS);
    if (!strcmp(name,"profileLevels")) { assert(!strcmp(sig,"[Landroid/media/MediaCodecInfo$CodecProfileLevel;")); return (void *)1; }
    if (!strcmp(name,"colorFormats")) { assert(!strcmp(sig,"[I")); return (void *)2; }
    assert(!strcmp(sig,"I"));
    if (!strcmp(name,"profile")) return (void *)3;
    assert(!strcmp(name,"level")); return (void *)4;
}
static Object *object_field(void *env, Object *obj, void *field) {
    (void)env; assert(obj->kind == CAPS);
    if ((uintptr_t)field == 1) return &profiles_array;
    assert((uintptr_t)field == 2); return &colors;
}
static int32_t int_field(void *env, Object *obj, void *field) {
    (void)env; assert(obj == &profile);
    if ((uintptr_t)field == 3) return 1;
    assert((uintptr_t)field == 4); return 256;
}
static Object *new_string(void *env, const uint16_t *units, int32_t length) {
    (void)env; assert(length >= 0 && length <= 512 && string_count < 512);
    int index = string_count++;
    strings[index].obj = (Object){STRING,0,strings[index].bytes};
    for (int i=0; i<length; i++) { assert(units[i] < 128); strings[index].bytes[i] = (char)units[i]; }
    strings[index].bytes[length] = 0; return &strings[index].obj;
}
static int32_t utf_length(void *env, Object *obj) { (void)env; assert(obj->kind == STRING); return (int32_t)strlen(obj->text); }
static const char *utf_chars(void *env, Object *obj, void *is_copy) { (void)env; (void)is_copy; assert(obj->kind == STRING); return obj->text; }
static void release_chars(void *env, Object *obj, const char *text) { (void)env; assert(obj->text == text); }
static int32_t array_length(void *env, Object *array) {
    (void)env;
    switch (array->kind) {
    case INFOS: return 6;
    case TYPES: return 1;
    case PROFILES: return mode == 3 ? 300 : 1;
    case COLORS: return 2;
    default: assert(0); return 0;
    }
}
static Object *array_element(void *env, Object *array, int32_t index) {
    (void)env; assert(index >= 0);
    if (array->kind == INFOS) { assert(index < 6); return infos + index; }
    assert(index == 0);
    if (array->kind == TYPES) return array->id == 5 ? &hevc : &avc;
    assert(array->kind == PROFILES); return &profile;
}
static void int_region(void *env, Object *array, int32_t start, int32_t count, int32_t *output) {
    (void)env; assert(array == &colors && start >= 0 && start < 2 && count == 1); *output = start ? 2130708361 : 19;
}
static int32_t push_frame(void *env, int32_t capacity) {
    (void)env; assert(capacity >= 32);
    if (mode == 5) { pending = 1; return -1; }
    assert(frame_count < 16); saved_strings[frame_count++] = string_count; return 0;
}
static Object *pop_frame(void *env, Object *result) { (void)env; assert(frame_count > 0 && !result); string_count = saved_strings[--frame_count]; return NULL; }
static Object *exception_occurred(void *env) { (void)env; return pending ? &exception : NULL; }
static void exception_clear(void *env) { (void)env; pending = 0; }
static void delete_ref(void *env, Object *obj) { (void)env; assert(obj); }
void *mock_jni_env(void) {
    for (int i=0; i<6; i++) { infos[i] = (Object){INFO,i,0}; caps[i] = (Object){CAPS,i,0}; types[i] = (Object){TYPES,i,0}; name_objects[i] = (Object){STRING,i,names[i]}; }
    table[6]=(void *)find_class; table[15]=(void *)exception_occurred; table[17]=(void *)exception_clear;
    table[19]=(void *)push_frame; table[20]=(void *)pop_frame; table[23]=(void *)delete_ref;
    table[30]=(void *)new_object; table[31]=(void *)get_class; table[33]=(void *)method_id;
    table[36]=(void *)object_call; table[39]=(void *)boolean_call; table[63]=(void *)void_call;
    table[94]=(void *)field_id; table[95]=(void *)object_field; table[100]=(void *)int_field;
    table[113]=(void *)method_id; table[116]=(void *)static_object_call;
    table[163]=(void *)new_string; table[168]=(void *)utf_length; table[169]=(void *)utf_chars; table[170]=(void *)release_chars;
    table[171]=(void *)array_length; table[173]=(void *)array_element; table[203]=(void *)int_region;
    return &env_table;
}
