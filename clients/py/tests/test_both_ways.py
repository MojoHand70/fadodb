"""The same tests, twice: over HTTP and in the same process (ADR-0101,
item 12; the test of Phase 9).

Over HTTP a server is started from the program FADODB_PROGRAM names, a
fadodb-core, with a user and a key for each caller. In the same process
the library FADODB_LIBRARY names is loaded, and each caller is said by
the program. The callers are the same people with the same groups, so
every answer must be the same. Without the program or the library the
tests of that way are skipped, unless FADODB_PYTHON is must, as on
GitHub, where a skipped test would not show.

    FADODB_PROGRAM=bin/fadodb-core FADODB_LIBRARY=bin/libfadodb-core.so \
        python -m unittest discover -s clients/py/tests
"""

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import unittest

# The package tried: the one beside these tests, or one a wheel was
# unpacked into, which FADODB_PYTHON_PACKAGE names (scripts/library.sh
# pack tries every wheel it makes).
sys.path.insert(0, os.environ.get("FADODB_PYTHON_PACKAGE") or os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

import fadodb  # noqa: E402

# The callers, the same people both ways: in the same process as these
# objects, over HTTP as users made with these groups.
CALLERS = {
    "dan": {"id": "dan", "kind": "person", "system_groups": ["designers"]},
    "ada": {"id": "ada", "kind": "person", "system_groups": ["administrators"]},
    "cleo": {"id": "cleo", "kind": "person", "system_groups": ["content-administrators"]},
    "ann": {"id": "ann", "kind": "person", "groups": ["sales"]},
    "bob": {"id": "bob", "kind": "person", "groups": ["accounting"]},
    "carol": {"id": "carol", "kind": "person", "groups": ["marketing"]},
    "ledger": {"id": "ledger-bot", "kind": "agent", "groups": ["accounting"]},
}

# The invoice of the manual's chapter on naming the caller: sales makes
# it, accounting books it, and only a person may.
INVOICE = {
    "name": "Invoice",
    "variables": {"number": {"type": "text", "required": True}, "account": {"type": "text"}},
    "states": {"initial": "open", "list": ["open", "booked"], "final": ["booked"]},
    "transitions": [{"name": "book", "from": "open", "to": "booked", "guard": "NOT ISBLANK(account)"}],
    "access": {
        "read": 'CREATEDBY() = USER() OR INGROUP("accounting")',
        "create": 'INGROUP("sales")',
        "update": 'INGROUP("accounting")',
        "delete": "FALSE",
        "transition": {"book": 'INGROUP("accounting") AND USERKIND() = "person"'},
    },
}

# A claim with the scan of its receipt.
CLAIM = {
    "name": "Claim",
    "variables": {"number": {"type": "text", "required": True}, "scan": {"type": "object", "schema": "File"}},
    "states": {"initial": "open", "list": ["open"]},
    "access": {"read": 'CREATEDBY() = USER() OR INGROUP("accounting")', "create": 'INGROUP("sales")',
               "update": "CREATEDBY() = USER()", "delete": "FALSE"},
}


# The shop of the chapter on the outbox: sending an order mails the
# customer and posts it to the books, whose entry comes back into it.
ORDER = {
    "name": "Order",
    "variables": {"customer": {"type": "text", "required": True}, "amount": {"type": "number", "required": True},
                  "entry": {"type": "text"}},
    "states": {"initial": "new", "list": ["new", "sent"]},
    "transitions": [{"name": "send", "from": "new", "to": "sent"}],
    "rules": [{"on": "transition", "name": "send", "do": [
        {"action": "mail.send", "args": {"to": "customer"}},
        {"action": "books.post", "args": {"amount": "amount"}, "into": {"entry": "entry"}}]}],
    "access": {"read": 'INGROUP("everyone")', "create": 'INGROUP("everyone")', "update": "CREATEDBY() = USER()",
               "delete": "FALSE"},
}
MAIL_SEND = {"name": "mail.send", "params": {"to": "text"}}
BOOKS_POST = {"name": "books.post", "params": {"amount": "number"}, "returns": {"entry": "text"}}


def needed(what, variable):
    path = os.environ.get(variable)
    if path and os.path.exists(path):
        return os.path.abspath(path)
    if os.environ.get("FADODB_PYTHON") == "must":
        raise RuntimeError(variable + " names no " + what + ", and FADODB_PYTHON is must")
    raise unittest.SkipTest(variable + " names no " + what)


class BothWays:
    """The tests. A subclass gives self.db(name), the database as one of
    the callers."""

    @classmethod
    def define(cls):
        dan = cls.db("dan")
        dan.exec("define_class", {"definition": INVOICE})
        dan.exec("define_class", {"definition": CLAIM})

    def test_the_story(self):
        ann, bob, ledger = self.db("ann"), self.db("bob"), self.db("ledger")
        made = ann.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}})
        self.assertEqual(made["document"]["state"], "open")
        self.assertEqual(made["document"]["created_by"], "ann")

        with self.assertRaises(fadodb.Refused) as refused:
            bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
        self.assertEqual(refused.exception.code, "guard_false")
        self.assertEqual(refused.exception.hint, "the guard: NOT ISBLANK(account)")

        bob.exec("set", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}})
        with self.assertRaises(fadodb.Refused) as refused:
            ledger.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
        self.assertEqual(refused.exception.code, "forbidden")
        booked = bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
        self.assertEqual(booked["document"]["state"], "booked")

        events = bob.exec("events", {"class": "Invoice", "id": "FV/2026/11/0042"})["events"]
        self.assertEqual([e["actor_id"] for e in events], ["ann", "bob", "bob"])

    def test_refusals(self):
        ann = self.db("ann")
        with self.assertRaises(fadodb.Refused) as refused:
            ann.exec("get", {"class": "Invoice", "id": "none"})
        self.assertEqual(refused.exception.code, "unknown_document")
        with self.assertRaises(fadodb.Refused) as refused:
            ann.exec("Create", {})
        self.assertEqual((refused.exception.code, refused.exception.hint), ("unknown_command", "the command is create"))
        with self.assertRaises(fadodb.Refused) as refused:
            ann.exec("create", {"class": "Invoice", "colour": "red"})
        self.assertEqual((refused.exception.code, refused.exception.path), ("bad_json", "/colour"))
        # Carol of marketing may not make an invoice.
        with self.assertRaises(fadodb.Refused) as refused:
            self.db("carol").exec("create", {"class": "Invoice", "values": {"number": "X"}})
        self.assertEqual(refused.exception.code, "forbidden")
        # subscribe answers a stream, which one call cannot.
        with self.assertRaises(fadodb.Refused) as refused:
            ann.exec("subscribe", {})
        self.assertEqual(refused.exception.code, "not_supported")

    def test_a_definition_refused_with_its_findings(self):
        with self.assertRaises(fadodb.Refused) as refused:
            self.db("dan").exec("define_class", {"definition": {"name": "Broken", "variables": {"total": {"formula": "SUM("}}}})
        self.assertEqual(refused.exception.code, "bad_definition")
        findings = refused.exception.answer["diagnostics"]
        self.assertEqual(findings[0]["path"], "/variables/total/formula")

    def test_a_file(self):
        ann, bob = self.db("ann"), self.db("bob")
        ann.exec("create", {"class": "Claim", "id": "C-7", "values": {"number": "C-7"}})
        scan = b"%PDF-1.7 the taxi from the airport, 84.00 PLN\x00 and a zero byte"
        facts = ann.put_file(scan, name="taxi.pdf", media_type="application/pdf")
        self.assertEqual(facts, {"hash": hashlib.sha256(scan).hexdigest(), "name": "taxi.pdf",
                                 "media_type": "application/pdf", "size": len(scan)})
        ann.exec("set", {"class": "Claim", "id": "C-7", "values": {"scan": facts}})

        got, data = bob.get_file("Claim", "C-7", facts["hash"])
        self.assertEqual(data, scan)
        self.assertEqual(got, facts)
        with self.assertRaises(fadodb.Refused):
            self.db("carol").get_file("Claim", "C-7", facts["hash"])
        with self.assertRaises(fadodb.Refused):
            bob.get_file("Claim", "C-7", facts["hash"], version=1)
        with self.assertRaises(fadodb.Refused) as refused:
            bob.get_blob(facts["hash"], reason="curious")
        self.assertEqual(refused.exception.code, "not_allowed")
        got, data = self.db("cleo").get_blob(facts["hash"], reason="the auditor asked for every scan of November")
        self.assertEqual((got["hash"], got["size"], data), (facts["hash"], len(scan), scan))

        # A file of no known type.
        plain = ann.put_file(b"", name="empty")
        self.assertEqual(plain["size"], 0)

    def test_waiting_for_a_change(self):
        ann, bob = self.db("ann"), self.db("bob")
        now = bob.exec("wait_for_changes", {"classes": ["Invoice"]})["after"]

        def later():
            time.sleep(0.5)
            ann.exec("create", {"class": "Invoice", "id": "W-1", "values": {"number": "W-1"}})

        threading.Thread(target=later).start()
        began = time.monotonic()
        heard = bob.exec("wait_for_changes", {"classes": ["Invoice"], "after": now, "seconds": 20})
        self.assertLess(time.monotonic() - began, 15)
        self.assertEqual([e["doc_id"] for e in heard["events"]], ["W-1"])

    def test_many_threads(self):
        ann = self.db("ann")
        made, failures = [], []

        def make(thread):
            for i in range(10):
                try:
                    ann.exec("create", {"class": "Invoice", "id": f"T{thread}-{i}", "values": {"number": "T"}})
                    made.append(1)
                except Exception as failure:  # noqa: BLE001
                    failures.append(failure)

        threads = [threading.Thread(target=make, args=(t,)) for t in range(8)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(failures, [])
        self.assertEqual(len(made), 80)
        counted = self.db("bob").exec("count", {"class": "Invoice", "where": 'number = "T"'})
        self.assertEqual(counted["count"], 80)


class InTheSameProcess(BothWays, unittest.TestCase):
    """Through the library of C."""

    @classmethod
    def setUpClass(cls):
        # A wheel for a system holds its library, which fadodb.open finds
        # beside the package; otherwise FADODB_LIBRARY names one.
        packaged = os.path.join(os.path.dirname(fadodb.__file__), "lib")
        if not os.environ.get("FADODB_LIBRARY") and os.path.isdir(packaged) and os.listdir(packaged):
            library = None
        else:
            library = needed("library", "FADODB_LIBRARY")
        cls.dir = tempfile.mkdtemp(prefix="fadodb-python-")
        cls.opened = fadodb.open(os.path.join(cls.dir, "books.fado"), library=library)
        cls.define()

    @classmethod
    def tearDownClass(cls):
        cls.opened.close()
        shutil.rmtree(cls.dir, ignore_errors=True)

    @classmethod
    def db(cls, name):
        return cls.opened.as_caller(CALLERS[name])

    def test_the_options_and_the_caller_are_checked(self):
        with self.assertRaises(fadodb.Refused) as refused:
            fadodb.open(os.path.join(self.dir, "other.fado"), options={"time_zone": "UTC"})
        self.assertEqual((refused.exception.code, refused.exception.path), ("bad_json", "/time_zone"))
        with self.assertRaises(fadodb.Refused) as refused:
            self.opened.as_caller({"id": "ann", "name": "Ann"}).exec("get", {"class": "Invoice", "id": "x"})
        self.assertEqual((refused.exception.code, refused.exception.path), ("bad_json", "/name"))

    def test_a_closed_database(self):
        other = fadodb.open(os.path.join(self.dir, "closed.fado"))
        other.close()
        other.close()
        with self.assertRaises(fadodb.Refused) as refused:
            other.exec("list_classes")
        self.assertEqual(refused.exception.code, "closed")

    def shop(self, name, actions, outbox=None):
        """A shop whose actions are functions of this program."""
        shop = fadodb.open(os.path.join(self.dir, name), options={"outbox": outbox or {"manual": True}}, actions=actions)
        self.addCleanup(shop.close)
        dan = shop.as_caller(CALLERS["dan"])
        dan.exec("define_action", {"definition": MAIL_SEND})
        dan.exec("define_action", {"definition": BOOKS_POST})
        dan.exec("define_class", {"definition": ORDER})
        ann = shop.as_caller(CALLERS["ann"])
        ann.exec("create", {"class": "Order", "id": "O-1", "values": {"customer": "jan@example.com", "amount": 120}})
        ann.exec("transition", {"class": "Order", "id": "O-1", "transition": "send"})
        return shop

    def test_the_programs_own_actions(self):
        heard = []

        def send(call):
            heard.append(call)

        def post(call):
            heard.append(call)
            return {"entry": "JE-2026-0007"}

        shop = self.shop("shop.fado", {"mail.send": send, "books.post": post})
        shop.as_caller(CALLERS["ada"]).exec("drain")
        self.assertEqual([c["action"] for c in heard], ["mail.send", "books.post"])
        self.assertEqual(heard[0]["args"], {"to": "jan@example.com"})
        self.assertEqual(heard[0]["event"]["document"]["state"], "sent")
        order = shop.as_caller(CALLERS["ann"]).exec("get", {"class": "Order", "id": "O-1"})["document"]
        self.assertEqual(order["values"]["entry"], "JE-2026-0007")

    def test_when_the_programs_action_fails(self):
        def post(call):
            raise fadodb.Permanent("the period 2026-11 is closed")

        shop = self.shop("refused.fado", {"mail.send": lambda call: None, "books.post": post})
        ada = shop.as_caller(CALLERS["ada"])
        ada.exec("drain")
        rows = {row["action"]: row for row in ada.exec("outbox", {"id": "O-1"})["actions"]}
        self.assertEqual((rows["mail.send"]["status"], rows["books.post"]["status"]), ("done", "dead"))
        self.assertEqual(rows["books.post"]["error_message"], "the period 2026-11 is closed")

        def send(call):
            raise ConnectionError("the mail server is down")

        shop = self.shop("failed.fado", {"mail.send": send, "books.post": lambda call: {"entry": "x"}})
        ada = shop.as_caller(CALLERS["ada"])
        ada.exec("drain")
        rows = {row["action"]: row for row in ada.exec("outbox", {"id": "O-1"})["actions"]}
        self.assertEqual(rows["mail.send"]["status"], "pending")
        self.assertEqual(rows["mail.send"]["error_message"], "ConnectionError: the mail server is down")

        with self.assertRaises(TypeError):
            fadodb.open(os.path.join(self.dir, "twice.fado"), options={"actions": ["mail.send"]},
                        actions={"mail.send": send})

    def test_the_worker_calls_from_its_threads(self):
        threads = set()

        def post(call):
            threads.add(threading.get_ident())
            return {"entry": "JE-2026-0008"}

        shop = self.shop("worker.fado", {"mail.send": lambda call: threads.add(threading.get_ident()),
                                         "books.post": post}, outbox={"workers": 2})
        ann = shop.as_caller(CALLERS["ann"])
        began = time.monotonic()
        while time.monotonic() - began < 20:
            if ann.exec("get", {"class": "Order", "id": "O-1"})["document"]["values"]["entry"]:
                break
            time.sleep(0.1)
        self.assertEqual(ann.exec("get", {"class": "Order", "id": "O-1"})["document"]["values"]["entry"], "JE-2026-0008")
        self.assertNotIn(threading.get_ident(), threads)

    def test_the_version(self):
        from fadodb import _library

        self.assertTrue(_library.version())


class OverHTTP(BothWays, unittest.TestCase):
    """Through a server that serves the database."""

    @classmethod
    def setUpClass(cls):
        program = needed("program", "FADODB_PROGRAM")
        cls.dir = tempfile.mkdtemp(prefix="fadodb-python-")
        file = os.path.join(cls.dir, "books.fado")

        def run(*args):
            return subprocess.run([program, *args], check=True, capture_output=True, text=True).stdout

        run("init", file)
        cls.keys = {}
        for name, caller in CALLERS.items():
            args = ["user", "add", caller["id"], "--kind", caller["kind"], "--db", file]
            for group in caller.get("groups", []):
                args += ["--group", group]
            for group in caller.get("system_groups", []):
                args += ["--system", group]
            run(*args)
            told = run("key", "new", caller["id"], "--label", "tests", "--db", file)
            cls.keys[name] = re.search(r"^(fado_\S+)$", told, re.M).group(1)

        cls.server = subprocess.Popen([program, "serve", file, "--listen", "127.0.0.1:0"],
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        cls.url = None
        began = time.monotonic()
        while cls.url is None and time.monotonic() - began < 30:
            line = cls.server.stdout.readline()
            if not line:
                break
            found = re.search(r"serving \S+ at (http://\S+)", line)
            if found:
                cls.url = found.group(1)
        if cls.url is None:
            cls.server.kill()
            raise RuntimeError("the server did not say where it serves")
        # The rest of what it says is kept, so that it never waits on a
        # full pipe, and shown when the tests end on GitHub, where it is
        # the only account of a request that got no answer.
        cls.said = []

        def keep():
            for line in cls.server.stdout:
                cls.said.append(line.rstrip("\n"))

        threading.Thread(target=keep, daemon=True).start()
        cls.define()

    @classmethod
    def tearDownClass(cls):
        cls.server.terminate()
        try:
            cls.server.wait(timeout=20)
        except subprocess.TimeoutExpired:
            cls.server.kill()
        if os.environ.get("FADODB_PYTHON") == "must":
            print("\nwhat the server said:", *cls.said[-60:], sep="\n  ", file=sys.stderr)
        shutil.rmtree(cls.dir, ignore_errors=True)

    @classmethod
    def db(cls, name):
        return fadodb.connect(cls.url, key=cls.keys[name])

    def test_the_key_says_who_calls(self):
        with self.assertRaises(TypeError):
            self.db("ann").as_caller(CALLERS["bob"])
        with self.assertRaises(fadodb.Refused) as refused:
            fadodb.connect(self.url, key="fado_NOTAKEY_0000").exec("list_classes")
        self.assertEqual(refused.exception.status, 401)

    def test_no_server(self):
        with self.assertRaises(fadodb.NoAnswer):
            fadodb.connect("http://127.0.0.1:9", key="k", timeout=5).exec("list_classes")


if __name__ == "__main__":
    unittest.main()
