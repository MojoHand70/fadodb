"""FadoDB for Python: a database over HTTP, or in the same process.

Two ways to the same database, and one way to use it (ADR-0101, step C):

    import fadodb

    # A server, as the user whose key it is:
    db = fadodb.connect("https://fado.acme.example", key="fado_Q329CE63C9_…")

    # Or the database file itself, in this process, through the library
    # of C; the program says who is calling:
    db = fadodb.open("books.fado", caller={"id": "ann", "groups": ["sales"]})

    invoice = db.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042",
                                 "values": {"number": "FV/2026/11/0042"}})["document"]
    try:
        db.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042",
                               "transition": "book"})
    except fadodb.Refused as refusal:
        print(refusal.code, refusal.message)    # guard_false …

A command is a name and one JSON object, and its answer is one JSON
object: the commands of the command set, the same as a server takes at
POST /v1/<command>. A refusal is raised as Refused, with the code a
program branches on. Nothing but the standard library is needed.
"""

from ._database import Database, NoAnswer, Refused, connect, open

__all__ = ["Database", "NoAnswer", "Refused", "connect", "open"]
