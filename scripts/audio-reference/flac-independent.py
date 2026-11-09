"""Decode native FLAC with libFLAC and verify its PCM MD5."""
import ctypes
import ctypes.util
from pathlib import Path


def decode(path, channels, bits, rate):
    library = ctypes.CDLL(ctypes.util.find_library('FLAC'))
    pointer = ctypes.c_void_p
    i32 = ctypes.c_int32
    library.FLAC__stream_decoder_new.restype = pointer
    library.FLAC__stream_decoder_set_md5_checking.argtypes = [pointer, ctypes.c_int]
    library.FLAC__stream_decoder_set_md5_checking.restype = ctypes.c_int
    library.FLAC__stream_decoder_init_file.argtypes = [pointer, ctypes.c_char_p, pointer, pointer, pointer, pointer]
    library.FLAC__stream_decoder_init_file.restype = ctypes.c_int
    library.FLAC__stream_decoder_process_until_end_of_stream.argtypes = [pointer]
    library.FLAC__stream_decoder_process_until_end_of_stream.restype = ctypes.c_int
    library.FLAC__stream_decoder_finish.argtypes = [pointer]
    library.FLAC__stream_decoder_finish.restype = ctypes.c_int
    library.FLAC__stream_decoder_delete.argtypes = [pointer]
    for field in ['channels', 'bits_per_sample', 'sample_rate']:
        function = getattr(library, 'FLAC__stream_decoder_get_' + field)
        function.argtypes = [pointer]
        function.restype = ctypes.c_uint
    errors, samples = [], []

    @ctypes.CFUNCTYPE(ctypes.c_int, pointer, pointer, ctypes.POINTER(ctypes.POINTER(i32)), pointer)
    def write(decoder, frame, buffers, client):
        # FLAC__Frame starts with FLAC__FrameHeader.blocksize, an unsigned int.
        count = ctypes.cast(frame, ctypes.POINTER(ctypes.c_uint))[0]
        if not 1 <= count <= 65535 or library.FLAC__stream_decoder_get_channels(decoder) != channels:
            errors.append('unexpected decoded frame geometry')
            return 1
        for at in range(count):
            for channel in range(channels):
                samples.append(buffers[channel][at])
        return 0

    @ctypes.CFUNCTYPE(None, pointer, ctypes.c_int, pointer)
    def error(decoder, code, client):
        errors.append(code)

    decoder = library.FLAC__stream_decoder_new()
    assert decoder
    try:
        assert library.FLAC__stream_decoder_set_md5_checking(decoder, 1)
        assert library.FLAC__stream_decoder_init_file(decoder, str(Path(path)).encode(),
                                                    ctypes.cast(write, pointer), None,
                                                    ctypes.cast(error, pointer), None) == 0
        assert library.FLAC__stream_decoder_process_until_end_of_stream(decoder), errors
        assert library.FLAC__stream_decoder_get_channels(decoder) == channels
        assert library.FLAC__stream_decoder_get_bits_per_sample(decoder) == bits
        assert library.FLAC__stream_decoder_get_sample_rate(decoder) == rate
        assert library.FLAC__stream_decoder_finish(decoder), 'libFLAC rejected the PCM MD5'
        assert not errors, errors
    finally:
        library.FLAC__stream_decoder_delete(decoder)
    version = ctypes.c_char_p.in_dll(library, 'FLAC__VERSION_STRING').value.decode()
    return samples, version
