"""A database held in this process, through the library of C (ADR-0101)."""

import ctypes
import ctypes.util
import json
import os
import sys
import threading
import weakref

# The file the library is on each system.
if sys.platform == "win32":
    _NAME = "fadodb-core.dll"
elif sys.platform == "darwin":
    _NAME = "libfadodb-core.dylib"
else:
    _NAME = "libfadodb-core.so"

# A process holds one library: a second Go library beside it would not
# work (ADR-0101, item 11). It is loaded the first time it is asked for.
_loaded = None
_loading = threading.Lock()


def _load(path):
    global _loaded
    with _loading:
        if _loaded is not None:
            if path is not None and os.path.abspath(path) != _loaded[0]:
                raise RuntimeError("this process already holds the library " + _loaded[0]
                                   + ", and a process holds one library")
            return _loaded[1]
        candidates = [path] if path is not None else [
            os.environ.get("FADODB_LIBRARY"),
            os.path.join(os.path.dirname(os.path.abspath(__file__)), "lib", _NAME),
            ctypes.util.find_library("fadodb-core"),
        ]
        tried = []
        for candidate in candidates:
            if not candidate:
                continue
            if os.sep in candidate or "/" in candidate:
                if not os.path.exists(candidate):
                    tried.append(candidate)
                    continue
                candidate = os.path.abspath(candidate)
            lib = ctypes.CDLL(candidate)
            _declare(lib)
            _loaded = (candidate, lib)
            return lib
        raise FileNotFoundError(
            "no library of FadoDB was found (" + ", ".join(tried or ["none was named"]) + "): "
            "give its path as library, or in the variable FADODB_LIBRARY; make library builds it as bin/" + _NAME)


def _declare(lib):
    u64, text, void = ctypes.c_uint64, ctypes.c_char_p, ctypes.c_void_p
    out, size = ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_size_t)
    lib.fado_open.argtypes = [text, text, out]
    lib.fado_open.restype = u64
    # The answers are kept as pointers, so that they are given back.
    lib.fado_exec.argtypes = [u64, text, text, text]
    lib.fado_exec.restype = void
    lib.fado_close.argtypes = [u64, out]
    lib.fado_close.restype = ctypes.c_int
    lib.fado_free.argtypes = [void]
    lib.fado_free.restype = None
    lib.fado_version.argtypes = []
    lib.fado_version.restype = text
    lib.fado_put_file.argtypes = [u64, text, text, text, ctypes.c_size_t]
    lib.fado_put_file.restype = void
    lib.fado_get_file.argtypes = [u64, text, text, out, size]
    lib.fado_get_file.restype = void
    lib.fado_get_blob.argtypes = [u64, text, text, out, size]
    lib.fado_get_blob.restype = void


def _take(lib, pointer):
    """The text of an answer of the library, which is then given back."""
    if not pointer:
        return ""
    try:
        return ctypes.string_at(pointer).decode("utf-8")
    finally:
        lib.fado_free(pointer)


def version(library=None):
    """The version of the library, such as v0.9.0, or dev."""
    return _load(library).fado_version().decode("utf-8")


class Opened:
    """One database the library opened, shared by every caller it is
    given to."""

    in_process = True

    def __init__(self, path, options, library):
        self._lib = _load(library)
        refusal = ctypes.c_void_p()
        options_text = json.dumps(options or {}, ensure_ascii=False).encode("utf-8")
        self._n = self._lib.fado_open(os.fspath(path).encode("utf-8"), options_text, ctypes.byref(refusal))
        if self._n == 0:
            from ._database import Refused

            raise Refused(json.loads(_take(self._lib, refusal.value)))
        # A database nobody holds any more is closed, as a file is.
        self._finalizer = weakref.finalize(self, _close, self._lib, self._n)

    def exec(self, caller, command, object_text):
        answer = self._lib.fado_exec(self._n, caller.encode("utf-8"), command.encode("utf-8"), object_text.encode("utf-8"))
        return None, _take(self._lib, answer)

    def put_file(self, caller, name, media_type, data):
        asked = {}
        if name is not None:
            asked["name"] = name
        if media_type is not None:
            asked["media_type"] = media_type
        object_text = json.dumps(asked, ensure_ascii=False).encode("utf-8")
        answer = self._lib.fado_put_file(self._n, caller.encode("utf-8"), object_text, data, len(data))
        return None, _take(self._lib, answer)

    def get_file(self, caller, asked):
        return self._get(self._lib.fado_get_file, caller, asked)

    def get_blob(self, caller, asked):
        return self._get(self._lib.fado_get_blob, caller, asked)

    def _get(self, function, caller, asked):
        data, length = ctypes.c_void_p(), ctypes.c_size_t()
        object_text = json.dumps(asked, ensure_ascii=False).encode("utf-8")
        answer = function(self._n, caller.encode("utf-8"), object_text, ctypes.byref(data), ctypes.byref(length))
        got = None
        if data.value:
            try:
                got = ctypes.string_at(data.value, length.value)
            finally:
                self._lib.fado_free(data.value)
        return None, _take(self._lib, answer), got

    def close(self):
        self._finalizer()


def _close(lib, n):
    lib.fado_close(n, None)
