"""Offline public LittleCMS API observations for one frozen profile, not an app dependency."""
import ctypes
import hashlib
import math
from pathlib import Path

PROFILE_SHA256 = "3bccbfedf06978c3035b255167afcf462a50b27805096cee038ff4dd69c650a6"
TYPE_RGB_DBL = (1 << 22) | (4 << 16) | (3 << 3)
TYPE_XYZ_DBL = (1 << 22) | (9 << 16) | (3 << 3)
FLAGS = 0x0100 | 0x0040 | 0x0004


def library_path():
    import PIL
    libraries = sorted((Path(PIL.__file__).resolve().parent.parent / "pillow.libs").glob("*lcms2*.so*"))
    if len(libraries) != 1:
        raise ValueError("expected the pinned Pillow LittleCMS library")
    return libraries[0]


def observe(profile, inputs):
    from PIL import features, __version__
    if __version__ != "12.3.0" or features.version_module("littlecms2") != "2.19":
        raise ValueError("offline CMM version")
    if hashlib.sha256(profile).hexdigest() != PROFILE_SHA256:
        raise ValueError("offline frozen profile identity")
    if not 0 < len(inputs) <= 4096 or any(len(row) != 3 or any(not math.isfinite(x) or not 0 <= x <= 1 for x in row) for row in inputs):
        raise ValueError("offline CMM input shape or domain")
    library = library_path()
    lib = ctypes.CDLL(str(library))
    pointer, unsigned = ctypes.c_void_p, ctypes.c_uint32
    for name, args, result in [
        ("cmsGetEncodedCMMversion", [], ctypes.c_int),
        ("cmsOpenProfileFromMem", [pointer, unsigned], pointer),
        ("cmsCreateXYZProfile", [], pointer),
        ("cmsCreateTransform", [pointer, unsigned, pointer, unsigned, unsigned, unsigned], pointer),
        ("cmsDoTransform", [pointer, pointer, pointer, unsigned], None),
        ("cmsCloseProfile", [pointer], ctypes.c_int),
        ("cmsDeleteTransform", [pointer], None),
    ]:
        function = getattr(lib, name)
        function.argtypes, function.restype = args, result
    if lib.cmsGetEncodedCMMversion() != 2190:
        raise ValueError("offline encoded CMM version")
    source = destination = transform = None
    buffer = ctypes.create_string_buffer(profile)
    try:
        source = lib.cmsOpenProfileFromMem(buffer, len(profile))
        if not source:
            raise ValueError("offline source profile")
        destination = lib.cmsCreateXYZProfile()
        if not destination:
            raise ValueError("offline XYZ profile")
        transform = lib.cmsCreateTransform(source, TYPE_RGB_DBL, destination, TYPE_XYZ_DBL, 1, FLAGS)
        if not transform:
            raise ValueError("offline relative XYZ transform")
        values = (ctypes.c_double * (len(inputs) * 3))(*[v for row in inputs for v in row])
        output = (ctypes.c_double * (len(inputs) * 3))()
        lib.cmsDoTransform(transform, values, output, len(inputs))
        observations = [list(output[3*i:3*i+3]) for i in range(len(inputs))]
        if any(not math.isfinite(v) for row in observations for v in row):
            raise ValueError("offline nonfinite XYZ observation")
        return observations, dict(Pillow=__version__, LittleCMS=features.version_module("littlecms2"), encoded_version=2190,
                                  library_sha256=hashlib.sha256(library.read_bytes()).hexdigest(),
                                  profile_sha256=PROFILE_SHA256, input_format=TYPE_RGB_DBL, output_format=TYPE_XYZ_DBL,
                                  intent=1, flags=FLAGS, black_point_compensation=False,
                                  scope="Finite offline frozen-profile observations only. No runtime CMM dependency or universal precision claim.")
    finally:
        if transform:
            lib.cmsDeleteTransform(transform)
        if destination:
            lib.cmsCloseProfile(destination)
        if source:
            lib.cmsCloseProfile(source)
