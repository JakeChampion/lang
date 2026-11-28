"""Offline public LittleCMS observations from XYZ to the frozen sRGB profile, not an app dependency."""
import ctypes
import hashlib
import math

from reference_icc_matrix_trc import PROFILE_SHA256, TYPE_XYZ_DBL, FLAGS, library_path

TYPE_RGB_8 = (4 << 16) | (3 << 3) | 1


def observe(profile, inputs):
    from PIL import features, __version__
    if __version__ != "12.3.0" or features.version_module("littlecms2") != "2.19":
        raise ValueError("offline CMM version")
    if hashlib.sha256(profile).hexdigest() != PROFILE_SHA256:
        raise ValueError("offline frozen profile identity")
    if not inputs or any(len(row) != 3 or any(not math.isfinite(x) or not 0 <= x <= 2 for x in row) for row in inputs):
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
        source = lib.cmsCreateXYZProfile()
        if not source:
            raise ValueError("offline XYZ profile")
        destination = lib.cmsOpenProfileFromMem(buffer, len(profile))
        if not destination:
            raise ValueError("offline destination profile")
        transform = lib.cmsCreateTransform(source, TYPE_XYZ_DBL, destination, TYPE_RGB_8, 1, FLAGS)
        if not transform:
            raise ValueError("offline relative sRGB transform")
        values = (ctypes.c_double * (len(inputs) * 3))(*[v for row in inputs for v in row])
        output = (ctypes.c_uint8 * (len(inputs) * 3))()
        lib.cmsDoTransform(transform, values, output, len(inputs))
        observations = [list(output[3*i:3*i+3]) for i in range(len(inputs))]
        return observations, dict(Pillow=__version__, LittleCMS=features.version_module("littlecms2"), encoded_version=2190,
                                  library_sha256=hashlib.sha256(library.read_bytes()).hexdigest(),
                                  profile_sha256=PROFILE_SHA256, input_format=TYPE_XYZ_DBL, output_format=TYPE_RGB_8,
                                  intent=1, flags=FLAGS, black_point_compensation=False,
                                  scope="Finite offline frozen-profile observations only. No runtime CMM dependency or universal precision claim.")
    finally:
        if transform:
            lib.cmsDeleteTransform(transform)
        if destination:
            lib.cmsCloseProfile(destination)
        if source:
            lib.cmsCloseProfile(source)
