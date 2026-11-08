# Independent WavPack 5.8.1 qualification through its public C API.
# Source: 4827b9889665b937b6ed71b9c6c0123152cd7a02.
import ctypes as C
import pathlib

lib = C.CDLL('libwavpack.so.1')
lib.WavpackGetLibraryVersionString.restype=C.c_char_p
assert lib.WavpackGetLibraryVersionString()==b'5.8.1'
class Config(C.Structure):
    _fields_=[('bitrate',C.c_float),('shaping_weight',C.c_float)]+[(n,C.c_int) for n in ['bits_per_sample','bytes_per_sample','qmode','flags','xmode','num_channels','float_norm_exp','block_samples','worker_threads','sample_rate','channel_mask']]+[('md5_checksum',C.c_ubyte*16),('md5_read',C.c_ubyte),('num_tag_strings',C.c_int),('tag_strings',C.c_void_p)]
assert C.sizeof(Config)==88
CALL=C.CFUNCTYPE(C.c_int,C.c_void_p,C.c_void_p,C.c_int32)
lib.WavpackOpenFileOutput.argtypes=[CALL,C.c_void_p,C.c_void_p];lib.WavpackOpenFileOutput.restype=C.c_void_p
lib.WavpackSetConfiguration64.argtypes=[C.c_void_p,C.POINTER(Config),C.c_int64,C.c_void_p]
lib.WavpackPackInit.argtypes=[C.c_void_p]
lib.WavpackPackSamples.argtypes=[C.c_void_p,C.POINTER(C.c_int32),C.c_uint32]
lib.WavpackFlushSamples.argtypes=[C.c_void_p]
lib.WavpackCloseFile.argtypes=[C.c_void_p];lib.WavpackCloseFile.restype=C.c_void_p
lib.WavpackOpenFileInput.argtypes=[C.c_char_p,C.c_char_p,C.c_int,C.c_int];lib.WavpackOpenFileInput.restype=C.c_void_p
lib.WavpackUnpackSamples.argtypes=[C.c_void_p,C.POINTER(C.c_int32),C.c_uint32]
lib.WavpackGetNumErrors.argtypes=[C.c_void_p]
for name in ['WavpackGetNumChannels','WavpackGetBitsPerSample','WavpackGetSampleRate','WavpackGetMode','WavpackGetFloatNormExp','WavpackGetChannelMask']:
    getattr(lib,name).argtypes=[C.c_void_p]
lib.WavpackGetChannelMask.restype=C.c_uint32
lib.WavpackGetErrorMessage.argtypes=[C.c_void_p]
lib.WavpackGetErrorMessage.restype=C.c_char_p
lib.WavpackGetTagItem.argtypes=[C.c_void_p,C.c_char_p,C.c_char_p,C.c_int]

def encode(samples,channels,bits,rate,flags=0,block=128,floating=False):
    chunks=[]
    @CALL
    def output(_,p,n):
        chunks.append(C.string_at(p,n));return 1
    ctx=lib.WavpackOpenFileOutput(output,None,None);assert ctx
    config=Config();config.bits_per_sample=bits;config.bytes_per_sample=(bits+7)//8;config.num_channels=channels;config.sample_rate=rate;config.flags=flags|(0x80 if floating else 0);config.float_norm_exp=127 if floating else 0;config.block_samples=block;config.channel_mask={1:4,2:3,3:7,4:0x33,5:0x37,6:0x3f,7:0x70f,8:0x63f}.get(channels,0)
    data=(C.c_int32*len(samples))(*samples)
    assert lib.WavpackSetConfiguration64(ctx,C.byref(config),len(samples)//channels,None),lib.WavpackGetErrorMessage(ctx)
    assert lib.WavpackPackInit(ctx)
    assert lib.WavpackPackSamples(ctx,data,len(samples)//channels)
    assert lib.WavpackFlushSamples(ctx)
    lib.WavpackCloseFile(ctx)
    return b''.join(chunks)

def verify(path,samples,channels):
    error=C.create_string_buffer(100)
    ctx=lib.WavpackOpenFileInput(str(path).encode(),error,2,0);assert ctx,error.value
    out=(C.c_int32*len(samples))()
    assert lib.WavpackUnpackSamples(ctx,out,len(samples)//channels)==len(samples)//channels
    assert lib.WavpackGetNumErrors(ctx)==0
    info={key:getattr(lib,'WavpackGet'+name)(ctx) for key,name in [('channels','NumChannels'),('depth','BitsPerSample'),('rate','SampleRate'),('mode','Mode'),('norm_exp','FloatNormExp'),('channel_mask','ChannelMask')]}
    title=C.create_string_buffer(100)
    lib.WavpackGetTagItem(ctx,b'Title',title,100)
    info['title']=title.value.decode()
    lib.WavpackCloseFile(ctx)
    assert list(out)==samples
    return info
