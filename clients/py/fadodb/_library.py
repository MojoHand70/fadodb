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


# The program's function for its actions, as fadodb.h declares it.
_ACTION = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_uint64, ctypes.c_char_p, ctypes.c_char_p)


def _declare(lib):
    u64, text, void = ctypes.c_uint64, ctypes.c_char_p, ctypes.c_void_p
    out, size = ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_size_t)
    lib.fado_open.argtypes = [text, text, out]
    lib.fado_open.restype = u64
    lib.fado_open_with.argtypes = [text, text, _ACTION, void, out]
    lib.fado_open_with.restype = u64
    lib.fado_answer.argtypes = [u64, text]
    lib.fado_answer.restype = ctypes.c_int
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

    def __init__(self, path, options, library, actions=None):
        from ._database import Refused

        self._lib = _load(library)
        options = dict(options or {})
        carry_out = _ACTION()
        if actions:
            if "actions" in options:
                raise TypeError("the actions are given once: as functions in actions, not as names in options")
            for name, function in actions.items():
                if not callable(function):
                    raise TypeError("the action " + name + " is given no function")
            options["actions"] = sorted(actions)
            carry_out = _ACTION(_carrier(self._lib, dict(actions)))
        refusal = ctypes.c_void_p()
        options_text = json.dumps(options, ensure_ascii=False).encode("utf-8")
        self._n = self._lib.fado_open_with(os.fspath(path).encode("utf-8"), options_text, carry_out, None, ctypes.byref(refusal))
        if self._n == 0:
            raise Refused(json.loads(_take(self._lib, refusal.value)))
        # A database nobody holds any more is closed, as a file is; the
        # function for its actions lives as long as it.
        self._finalizer = weakref.finalize(self, _close, self._lib, self._n, carry_out)

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


def _close(lib, n, carry_out=None):
    lib.fado_close(n, None)


def _carrier(lib, actions):
    """The function the library calls for an action the program carries
    out: it runs the program's function and answers with fado_answer."""
    from ._database import Permanent

    def carry_out(_context, call, action, object_text):
        name = action.decode("utf-8")
        try:
            result = actions[name](json.loads(object_text.decode("utf-8")))
            if result is None:
                result = {}
            if not isinstance(result, dict):
                raise TypeError("the function for " + name + " returned " + type(result).__name__
                                + ", and the result of an action is a dict of what it returns")
            answer = {"result": result}
        except Permanent as refusal:
            answer = {"error": {"message": str(refusal) or name + " was refused", "permanent": True}}
        except Exception as failure:  # noqa: BLE001 - a failure of the program's function is tried again
            answer = {"error": {"message": type(failure).__name__ + ": " + str(failure)}}
        try:
            text = json.dumps(answer, ensure_ascii=False)
        except (TypeError, ValueError) as failure:
            text = json.dumps({"error": {"message": "the result of " + name + " is no JSON: " + str(failure)}})
        lib.fado_answer(call, text.encode("utf-8"))

    return carry_out
