"""What one command costs from Python, for the page of v0.9.0
(docs/perf/v0.9.0.md): in the same process through the library of C,
and over HTTP to a fadodb-core serve on this machine.

    FADODB_LIBRARY=… FADODB_PROGRAM=… python3 clients/py/measure.py DIR N ROUNDS

It prints one JSON object: for each way, the microseconds of one read
and of one make in each round, the microseconds the library took to
load and a database to open, and what one action costs when the
program's function carries it out.
"""

import http.client
import json
import os
import re
import urllib.parse
import subprocess
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fadodb  # noqa: E402

DAN = {"id": "dan", "kind": "person", "system_groups": ["designers"]}
ADA = {"id": "ada", "kind": "person", "system_groups": ["administrators"]}
ANN = {"id": "ann", "kind": "person"}
NOTE = {
    "name": "Note",
    "variables": {"text": {"type": "text"}},
    "states": {"initial": "open", "list": ["open"]},
    "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"},
}
# A note that, once sent, posts itself to the books: one action, which
# the program's function carries out.
SENT = {
    "name": "Sent",
    "variables": {"entry": {"type": "text"}},
    "states": {"initial": "new", "list": ["new", "sent"]},
    "transitions": [{"name": "send", "from": "new", "to": "sent"}],
    "rules": [{"on": "transition", "name": "send", "do": [{"action": "books.post", "into": {"entry": "entry"}}]}],
    "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE", "transition": {"send": "TRUE"}},
}
POST = {"name": "books.post", "returns": {"entry": "text"}}


def rounds_of(db, n, rounds):
    ann = db
    ann.exec("create", {"class": "Note", "id": "N-0", "values": {"text": "the one read"}})
    made, out = 0, {"get": [], "create": []}
    for _ in range(rounds):
        began = time.perf_counter()
        for _ in range(n):
            ann.exec("get", {"class": "Note", "id": "N-0"})
        out["get"].append((time.perf_counter() - began) / n * 1e6)
        began = time.perf_counter()
        for _ in range(n):
            ann.exec("create", {"class": "Note", "id": f"M-{made}", "values": {"text": "a note"}})
            made += 1
        out["create"].append((time.perf_counter() - began) / n * 1e6)
    return out


def in_process(folder, n, rounds):
    began = time.perf_counter()
    from fadodb import _library

    _library._load(os.environ["FADODB_LIBRARY"])
    loaded = (time.perf_counter() - began) * 1e6
    began = time.perf_counter()
    db = fadodb.open(os.path.join(folder, "python.fado"))
    opened = (time.perf_counter() - began) * 1e6
    db.as_caller(DAN).exec("define_class", {"definition": NOTE})
    out = rounds_of(db.as_caller(ANN), n, rounds)
    db.close()
    out.update(load=loaded, open=opened)
    return out


def actions(folder, n, rounds):
    """One action through the program's function: n notes sent, then
    the queue run, in each round."""
    shop = fadodb.open(os.path.join(folder, "actions.fado"), options={"outbox": {"manual": True}},
                       actions={"books.post": lambda call: {"entry": "JE-" + call["key"][-6:]}})
    shop.as_caller(DAN).exec("define_action", {"definition": POST})
    shop.as_caller(DAN).exec("define_class", {"definition": SENT})
    ann, ada = shop.as_caller(ANN), shop.as_caller(ADA)
    out, made = [], 0
    for _ in range(rounds):
        for _ in range(n):
            ann.exec("create", {"class": "Sent", "id": f"S-{made}"})
            ann.exec("transition", {"class": "Sent", "id": f"S-{made}", "transition": "send"})
            made += 1
        began = time.perf_counter()
        ran = ada.exec("drain")["attempted"]
        out.append((time.perf_counter() - began) / max(ran, 1) * 1e6)
    shop.close()
    return {"drain_per_action": out}


def over_http(folder, n, rounds):
    program = os.environ["FADODB_PROGRAM"]
    file = os.path.join(folder, "served.fado")

    def run(*args):
        return subprocess.run([program, *args], check=True, capture_output=True, text=True).stdout

    run("init", file)
    keys = {}
    for who, groups in (("dan", ["--system", "designers"]), ("ann", [])):
        run("user", "add", who, "--kind", "person", "--db", file, *groups)
        keys[who] = re.search(r"^(fado_\S+)$", run("key", "new", who, "--label", "measure", "--db", file), re.M).group(1)
    server = subprocess.Popen([program, "serve", file, "--listen", "127.0.0.1:0"], stdout=subprocess.PIPE,
                              stderr=subprocess.STDOUT, text=True)
    url = None
    while url is None:
        line = server.stdout.readline()
        if not line:
            raise RuntimeError("the server said nothing")
        m = re.search(r"serving \S+ at (http://\S+)", line)
        url = m.group(1) if m else None
    threading.Thread(target=server.stdout.read, daemon=True).start()
    try:
        fadodb.connect(url, key=keys["dan"]).exec("define_class", {"definition": NOTE})
        client = rounds_of(fadodb.connect(url, key=keys["ann"]), n, rounds)
        return client, kept(url, keys["ann"], n, rounds)
    finally:
        server.terminate()
        server.wait(timeout=20)


class Kept:
    """The commands over one connection kept open, with Python's
    http.client: what a client that keeps its connection pays."""

    def __init__(self, url, key):
        where = urllib.parse.urlsplit(url)
        self.conn = http.client.HTTPConnection(where.hostname, where.port)
        self.key = key

    def exec(self, command, object=None):
        self.conn.request("POST", "/v1/" + command, body=json.dumps(object or {}),
                          headers={"Authorization": "Bearer " + self.key, "Content-Type": "application/json"})
        reply = self.conn.getresponse()
        answer = json.loads(reply.read())
        if "error" in answer:
            raise RuntimeError(answer)
        return answer


def kept(url, key, n, rounds):
    db = Kept(url, key)
    db.exec("create", {"class": "Note", "id": "K-0", "values": {"text": "the one read"}})
    made, out = 0, {"get": [], "create": []}
    for _ in range(rounds):
        began = time.perf_counter()
        for _ in range(n):
            db.exec("get", {"class": "Note", "id": "K-0"})
        out["get"].append((time.perf_counter() - began) / n * 1e6)
        began = time.perf_counter()
        for _ in range(n):
            db.exec("create", {"class": "Note", "id": f"KM-{made}", "values": {"text": "a note"}})
            made += 1
        out["create"].append((time.perf_counter() - began) / n * 1e6)
    db.conn.close()
    return out


def main():
    folder, n, rounds = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    client, kept_open = over_http(folder, n, rounds)
    print(json.dumps({"in_process": in_process(folder, n, rounds), "actions": actions(folder, n, rounds),
                      "http": client, "kept": kept_open}))


if __name__ == "__main__":
    main()
