# The story of the chapter "In a program of another language", in
# Python, in the same process (docs/manual/other-languages.md). Run it in
# a folder with invoice.json and nothing else:
#
#     python3 books.py
import json

import fadodb

with open("invoice.json", encoding="utf-8") as f:
    invoice = json.load(f)

db = fadodb.open("books.fado")
dan = db.as_caller({"id": "dan", "kind": "person", "system_groups": ["designers"]})
ann = db.as_caller({"id": "ann", "kind": "person", "groups": ["sales"]})
bob = db.as_caller({"id": "bob", "kind": "person", "groups": ["accounting"]})

dan.exec("define_class", {"definition": invoice})
print("dan defines the class Invoice")

made = ann.exec("create", {"class": "Invoice", "id": "FV/2026/11/0042",
                           "values": {"number": "FV/2026/11/0042"}})["document"]
print(f"ann makes {made['id']}: {made['state']}")

try:
    bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"})
except fadodb.Refused as refusal:
    print(f"bob books it: refused, {refusal.code}")

bob.exec("set", {"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}})
print("bob gives the account 400-01")

booked = bob.exec("transition", {"class": "Invoice", "id": "FV/2026/11/0042",
                                 "transition": "book"})["document"]
print(f"bob books it: {booked['state']}")

db.close()
