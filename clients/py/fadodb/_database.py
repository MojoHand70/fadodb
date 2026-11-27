"""The database a program uses, whichever way it reaches it."""

import json

__all__ = ["Database", "NoAnswer", "Permanent", "Refused", "connect", "open"]


class Refused(Exception):
    """A command the database did not carry out.

    code is what a program branches on, such as forbidden or guard_false;
    message says why in words, hint how to send it so that it is carried
    out, and path names the member of the object it is about. answer is
    the whole answer: a refused definition has its findings there, under
    diagnostics. status is the status of the reply over HTTP, and None in
    the same process.
    """

    def __init__(self, answer, status=None):
        error = answer.get("error") if isinstance(answer, dict) else None
        if not isinstance(error, dict):
            error = {"code": "internal", "message": "the answer is a refusal without an error"}
        self.answer = answer
        self.status = status
        self.code = error.get("code")
        self.message = error.get("message", "")
        self.hint = error.get("hint")
        self.path = error.get("path")
        super().__init__(f"{self.code}: {self.message}")


class NoAnswer(Exception):
    """No answer came through: the server was not reached, what answered
    is not a FadoDB server, or the answer was cut off. The command may
    have been carried out; one that carries a "key" is safely sent
    again."""


class Permanent(Exception):
    """Raised by the program's function for an action, a refusal: an
    answer that asking again would not change. The action is dead at
    once, with these words; any other exception is tried again."""


def connect(url, key, *, timeout=60.0):
    """The database a server serves at url, as the user whose key it is.

    Every command is judged by the rules of the database as that user's,
    and written to its log under that user. timeout is how long one
    command may take, in seconds, before NoAnswer.
    """
    from ._server import Server

    return Database(Server(url, key, timeout), None)


def open(path, *, caller=None, options=None, library=None, actions=None):
    """The database file at path, held in this process through the
    library of C, and made when it does not exist.

    caller is who the program says is calling, with the members a user
    has: {"id": "ann", "kind": "person", "groups": ["sales"],
    "system_groups": ["designers"], "today": "2026-11-27"}; None is
    nobody. The engine trusts it: the program has said who it is.
    Database.as_caller gives the same database to another caller.

    options are the options of fado_open, such as {"timezone":
    "Europe/Warsaw"}. library is the path of the library; when it is
    None, the variable FADODB_LIBRARY of the environment names it, or
    the one beside this package, or the one the system finds.

    actions are the actions the program carries out itself, a function
    by the name of each (ADR-0102): {"mail.send": send}. The function is
    called with the call, a dict as a webhook gets it, from a thread of
    the worker, and returns the action's result as a dict, or None for
    nothing; raising Permanent refuses the call, and any other exception
    fails it, to be tried again.
    """
    from ._library import Opened

    return Database(Opened(path, options, library, actions), caller)


# A command whose answer is a stream, which one call cannot answer.
_STREAMS = {"subscribe"}


class Database:
    """A database, over HTTP or in this process: the same commands, the
    same answers."""

    def __init__(self, door, caller):
        self._door = door
        self._caller = _caller_text(caller)

    @property
    def in_process(self):
        """Whether the database is held in this process."""
        return self._door.in_process

    def as_caller(self, caller):
        """The same database, for another caller. Only a database held
        in this process has a caller of the program's choosing: over
        HTTP, the key says who calls."""
        if not self._door.in_process:
            raise TypeError("over HTTP the key says who is calling: connect with that user's key")
        return Database(self._door, caller)

    def exec(self, command, object=None):
        """Runs one command and answers its answer, a dict. A refusal is
        raised as Refused."""
        if command in _STREAMS:
            raise Refused({"error": {
                "code": "not_supported",
                "message": command + " answers a stream of events, and exec answers one object",
                "hint": "wait_for_changes answers the same events, one call at a time"}})
        text = json.dumps({} if object is None else object, ensure_ascii=False)
        status, answer = self._door.exec(self._caller, command, text)
        return _answered(status, answer)

    def put_file(self, data, *, name=None, media_type=None):
        """Keeps the bytes of a file and answers its facts, {"hash": …,
        "name": …, "media_type": …, "size": …}, for a command to commit in
        a variable of the schema File."""
        if not isinstance(data, (bytes, bytearray, memoryview)):
            raise TypeError("the file is bytes")
        status, answer = self._door.put_file(self._caller, name, media_type, bytes(data))
        return _answered(status, answer)["file"]

    def get_file(self, class_, id, hash, *, version=None, for_=None, reason=None):
        """Reads a file a document holds, under the rules of the document:
        its facts and its bytes. version reads it as an earlier version of
        the document held it, for_ says for whom the caller acts, and
        reason is a content administrator's for reading past a rule."""
        asked = {"class": class_, "id": id, "hash": hash}
        if version is not None:
            asked["version"] = version
        if for_ is not None:
            asked["for"] = for_
        if reason is not None:
            asked["reason"] = reason
        status, answer, data = self._door.get_file(self._caller, asked)
        return _file(_answered(status, answer)["file"]), data

    def get_blob(self, hash, *, reason):
        """Reads bytes by their hash alone, which the engine does for a
        content administrator and nobody else, with a reason the log of
        reads keeps: their facts and their bytes."""
        status, answer, data = self._door.get_blob(self._caller, {"hash": hash, "reason": reason})
        return _file(_answered(status, answer)["file"]), data

    def close(self):
        """Closes the database: in this process the file, for every caller
        it was given to; over HTTP the connections. Closing again does
        nothing."""
        self._door.close()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


def _caller_text(caller):
    if caller is None:
        return ""
    if isinstance(caller, str):
        return caller
    if not isinstance(caller, dict):
        raise TypeError("the caller is a dict, such as {\"id\": \"ann\", \"groups\": [\"sales\"]}")
    return json.dumps(caller, ensure_ascii=False)


def _answered(status, text):
    try:
        answer = json.loads(text)
    except ValueError:
        raise NoAnswer("what answered is no JSON: " + text[:200]) from None
    if not isinstance(answer, dict):
        raise NoAnswer("what answered is no JSON object: " + text[:200])
    if "error" in answer:
        raise Refused(answer, status)
    return answer


def _file(facts):
    # A file of no known type is application/octet-stream, as a server
    # serves it.
    facts = dict(facts)
    if not facts.get("media_type"):
        facts["media_type"] = "application/octet-stream"
    return facts
