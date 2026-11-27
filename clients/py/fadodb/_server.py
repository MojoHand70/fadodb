"""A database a server serves, over HTTP (ADR-0055)."""

import email.message
import json
import urllib.error
import urllib.parse
import urllib.request

from ._database import NoAnswer


class Server:
    """The commands of one database, sent to its server as one user."""

    in_process = False

    def __init__(self, url, key, timeout):
        if not url.startswith(("http://", "https://")):
            raise ValueError("the address of a server begins with https:// or http://")
        self._base = url.rstrip("/") + "/v1/"
        self._key = key
        self._timeout = timeout

    def _send(self, method, address, body=None, content_type=None):
        headers = {"Authorization": "Bearer " + self._key, "Accept": "application/json"}
        if body is not None:
            # A body is sent with its own type, or none: never as a form,
            # which is what urllib would say it is.
            headers["Content-Type"] = content_type or ""
        request = urllib.request.Request(self._base + address, data=body, method=method, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=self._timeout) as reply:
                return reply.status, reply.headers, reply.read()
        except urllib.error.HTTPError as reply:
            try:
                return reply.code, reply.headers, reply.read()
            finally:
                reply.close()
        except (urllib.error.URLError, OSError) as failure:
            raise NoAnswer("the server at " + self._base + " was not reached: " + str(failure)) from failure

    def exec(self, caller, command, object_text):
        status, _, body = self._send("POST", urllib.parse.quote(command, safe=""), object_text.encode("utf-8"), "application/json")
        return status, body.decode("utf-8", "replace")

    def put_file(self, caller, name, media_type, data):
        address = "blobs"
        if name is not None:
            address += "?" + urllib.parse.urlencode({"name": name})
        status, _, body = self._send("POST", address, data, media_type)
        return status, body.decode("utf-8", "replace")

    def get_file(self, caller, asked):
        return self._get("file", asked)

    def get_blob(self, caller, asked):
        return self._get("blob", asked)

    def _get(self, address, asked):
        status, headers, body = self._send("GET", address + "?" + urllib.parse.urlencode(asked))
        if status != 200:
            return status, body.decode("utf-8", "replace"), None
        # The facts of the file are in the headers of the reply, as the
        # library answers them in JSON.
        facts = {"hash": (headers.get("ETag") or "").strip('"'), "size": len(body)}
        disposition = email.message.Message()
        disposition["Content-Disposition"] = headers.get("Content-Disposition", "")
        name = disposition.get_param("filename", header="Content-Disposition")
        if name:
            facts["name"] = name
        media_type = (headers.get("Content-Type") or "").split(";")[0].strip()
        if media_type:
            facts["media_type"] = media_type
        return status, json.dumps({"file": facts}), body

    def close(self):
        # Each request has its own connection, closed when it is
        # answered.
        pass
