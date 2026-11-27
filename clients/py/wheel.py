"""Writes the wheel of the Python client (ADR-0101, step D).

A wheel is a zip of the package and three files that say what it is
(PEP 427), so it is written here with Python's own zipfile, and a
release needs no tool fetched from the network to make one:

    python3 clients/py/wheel.py --version 0.9.0 --out bin/library
        fadodb-0.9.0-py3-none-any.whl: the client over HTTP; in the same
        process it finds the library as fadodb.open says

    python3 clients/py/wheel.py --version 0.9.0 --out bin/library \\
        --platform manylinux_2_35_x86_64 --library bin/library/libfadodb.so \\
        --library bin/library/libduckdb.so --license bin/library/LICENSE-duckdb
        fadodb-0.9.0-py3-none-manylinux_2_35_x86_64.whl: the same, with
        the libraries of that system in fadodb/lib, which fadodb.open finds

The name, the description and what Python is needed are read from
pyproject.toml, so the wheel says what the project says.
"""

import argparse
import base64
import hashlib
import os
import re
import sys
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPOSITORY = os.path.dirname(os.path.dirname(HERE))


def read_project(path):
    """The members of [project] and of [project.urls] in pyproject.toml,
    each a line key = "value", as this project writes them. tomllib is
    not in the Python of every machine a release is built on."""
    project, table = {"urls": {}}, None
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            header = re.fullmatch(r"\[([^\]]+)\]", line)
            if header:
                table = header.group(1)
                continue
            member = re.fullmatch(r'([A-Za-z_-]+)\s*=\s*"([^"]*)"', line)
            if member and table == "project":
                project[member.group(1)] = member.group(2)
            elif member and table == "project.urls":
                project["urls"][member.group(1)] = member.group(2)
    return project


def digest(data):
    return "sha256=" + base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()


def main():
    asked = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    asked.add_argument("--version", required=True, help="the version, such as 0.9.0; a leading v is left out")
    asked.add_argument("--out", required=True, help="the directory the wheel is written to")
    asked.add_argument("--library", action="append", default=[],
                       help="a library of C to put in fadodb/lib; libfadodb and DuckDB's beside it")
    asked.add_argument("--license", action="append", default=[],
                       help="the licence of what a library carries, such as DuckDB's, put beside the project's")
    asked.add_argument("--platform", default="any", help="the platform the library is for, as a wheel names it")
    told = asked.parse_args()
    if (not told.library) != (told.platform == "any"):
        asked.error("a wheel for a platform holds its library, and one for any platform none")

    project = read_project(os.path.join(HERE, "pyproject.toml"))
    name, version = project["name"], told.version[1:] if told.version.startswith("v") else told.version
    if version == "dev":
        # A build that is no release, under a version Python takes.
        version = "0.0.0.dev0"
    # A tag of a version before its release, v1.0.0-rc.1, is 1.0.0rc1 to
    # Python, and -alpha and -beta are a and b.
    pre = re.fullmatch(r"(\d+\.\d+\.\d+)-(alpha|beta|rc)\.?(\d+)", version)
    if pre:
        version = pre.group(1) + {"alpha": "a", "beta": "b", "rc": "rc"}[pre.group(2)] + pre.group(3)
    if not re.fullmatch(r"\d+\.\d+\.\d+((a|b|rc)\d+|\.dev\d+)?", version):
        asked.error("the version " + version + " is no version a wheel takes, such as 0.9.0 or 0.9.0rc1")
    with open(os.path.join(HERE, project["readme"]), encoding="utf-8") as f:
        readme = f.read()

    files = {}
    package = os.path.join(HERE, "fadodb")
    for entry in sorted(os.listdir(package)):
        if entry.endswith(".py"):
            with open(os.path.join(package, entry), "rb") as f:
                files["fadodb/" + entry] = f.read()
    for library in told.library:
        with open(library, "rb") as f:
            files["fadodb/lib/" + os.path.basename(library)] = f.read()

    info = f"{name}-{version}.dist-info"
    metadata = "\n".join([
        "Metadata-Version: 2.4",
        f"Name: {name}",
        f"Version: {version}",
        f"Summary: {project['description']}",
        f"Requires-Python: {project['requires-python']}",
        f"License-Expression: {project['license']}",
        "License-File: LICENSE",
        *(["License-File: THIRD_PARTY_NOTICES.md"] if told.library else []),
        *(f"License-File: {os.path.basename(path)}" for path in told.license),
        *(f"Project-URL: {k}, {v}" for k, v in project.get("urls", {}).items()),
        "Description-Content-Type: text/markdown",
        "",
        readme,
    ])
    files[f"{info}/METADATA"] = metadata.encode("utf-8")
    files[f"{info}/WHEEL"] = "\n".join([
        "Wheel-Version: 1.0",
        "Generator: fadodb clients/py/wheel.py",
        # A wheel with a library of C in it is for one platform.
        f"Root-Is-Purelib: {'true' if told.platform == 'any' else 'false'}",
        f"Tag: py3-none-{told.platform}",
        "",
    ]).encode("utf-8")
    with open(os.path.join(REPOSITORY, "LICENSE"), "rb") as f:
        files[f"{info}/licenses/LICENSE"] = f.read()
    # A wheel with a library carries the code of others the library is
    # built from, and their notices.
    if told.library:
        with open(os.path.join(REPOSITORY, "THIRD_PARTY_NOTICES.md"), "rb") as f:
            files[f"{info}/licenses/THIRD_PARTY_NOTICES.md"] = f.read()
    for path in told.license:
        with open(path, "rb") as f:
            files[f"{info}/licenses/{os.path.basename(path)}"] = f.read()
    record = "".join(f"{path},{digest(data)},{len(data)}\n" for path, data in files.items())
    files[f"{info}/RECORD"] = (record + f"{info}/RECORD,,\n").encode("utf-8")

    os.makedirs(told.out, exist_ok=True)
    wheel = os.path.join(told.out, f"{name}-{version}-py3-none-{told.platform}.whl")
    with zipfile.ZipFile(wheel, "w", zipfile.ZIP_DEFLATED) as z:
        for path, data in files.items():
            entry = zipfile.ZipInfo(path, date_time=(2026, 1, 1, 0, 0, 0))
            # A library is a program to its system: it keeps the right
            # to be run.
            entry.external_attr = (0o755 if path.startswith("fadodb/lib/") else 0o644) << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(entry, data)
    print(wheel)


if __name__ == "__main__":
    sys.exit(main())
