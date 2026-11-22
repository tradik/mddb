"""qdrant-client against MDDB's Qdrant-compatible point query (#447).

The acceptance test of the issue: the call Open WebUI's External Knowledge
Source (Qdrant provider) makes — QdrantClient(url=..., api_key=...).query_points
with the query embedded by the caller — answered by MDDB, read with Open
WebUI's default field mapping (payload.text, payload.metadata, id, score).

The suite starts the server itself from MDDB_BIN (a built mddbd) with auth on,
MDDB_QDRANT_COMPAT=true and an OpenAI-shaped embedding endpoint served by the
test, so MDDB and the "client" embed with the same model, as Open WebUI and
MDDB must. Nothing leaves the machine.
"""

import hashlib
import importlib.metadata
import json
import math
import os
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest
from qdrant_client import QdrantClient
from qdrant_client.common.version_check import get_server_version, is_compatible
from qdrant_client.http.exceptions import UnexpectedResponse

DIMS = 64
COLLECTION = "kb"
DOCS = {
    "owl": "The owl hunts mice at night in the forest",
    "fox": "The quick brown fox jumps over the lazy dog",
    "tea": "Green tea is brewed with water below boiling",
}


def embed(text):
    """A deterministic bag-of-words embedding: the stand-in for a shared model."""
    vec = [0.0] * DIMS
    for word in text.lower().split():
        vec[int.from_bytes(hashlib.blake2b(word.encode(), digest_size=4).digest(), "big") % DIMS] += 1.0
    norm = math.sqrt(sum(v * v for v in vec)) or 1.0
    return [v / norm for v in vec]


class _Embeddings(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802 - http.server's naming
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        inputs = body["input"] if isinstance(body["input"], list) else [body["input"]]
        out = json.dumps({"data": [{"index": i, "embedding": embed(t)} for i, t in enumerate(inputs)],
                          "model": body.get("model", "")}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *args):
        pass


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _call(base, path, body=None, token=None):
    req = urllib.request.Request(base + path, data=None if body is None else json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"null")


def _wait_healthy(base, proc):
    deadline = time.time() + 60
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"mddbd exited with {proc.returncode}")
        try:
            with urllib.request.urlopen(base + "/health", timeout=2) as r:
                if r.status == 200:
                    return
        except OSError:
            pass
        time.sleep(0.3)
    raise RuntimeError("mddbd did not become healthy within 60 s")


@pytest.fixture(scope="session")
def server():
    binary = os.environ.get("MDDB_BIN")
    if not binary:
        pytest.skip("set MDDB_BIN (a built mddbd)")
    fake = ThreadingHTTPServer(("127.0.0.1", 0), _Embeddings)
    threading.Thread(target=fake.serve_forever, daemon=True).start()
    port = _free_port()
    data = tempfile.mkdtemp(prefix="mddb-qdrant-")
    env = dict(os.environ, MDDB_QDRANT_COMPAT="true", MDDB_EMBEDDING_PROVIDER="openai",
               MDDB_EMBEDDING_API_URL=f"http://127.0.0.1:{fake.server_port}/v1", MDDB_EMBEDDING_API_KEY="test",
               MDDB_EMBEDDING_MODEL="bag-of-words", MDDB_EMBEDDING_DIMENSIONS=str(DIMS),
               MDDB_AUTH_ENABLED="true", MDDB_AUTH_JWT_SECRET="qdrant-compat-test-secret-0123456789",
               MDDB_AUTH_ADMIN_USERNAME="admin", MDDB_AUTH_ADMIN_PASSWORD="admin-pass-123",
               MDDB_PATH=os.path.join(data, "mddb.db"), MDDB_HTTP_ADDR=f"127.0.0.1:{port}",
               MDDB_GRPC_ENABLED="false", MDDB_MCP_ENABLED="false", MDDB_HTTP3_ENABLED="false")
    proc = subprocess.Popen([binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    base = f"http://127.0.0.1:{port}"
    try:
        _wait_healthy(base, proc)
        token = _call(base, "/v1/auth/login", {"username": "admin", "password": "admin-pass-123"})["token"]
        key = _call(base, "/v1/auth/api-key", {"description": "open-webui"}, token)["key"]
        for k, text in DOCS.items():
            _call(base, "/v1/add", {"collection": COLLECTION, "key": k, "lang": "en", "contentMd": text,
                                    "meta": {"title": [k.title()]}}, token)
        yield {"base": base, "key": key}
    finally:
        proc.terminate()
        proc.wait(timeout=30)
        fake.shutdown()


def _client(server, key=None):
    # Exactly what Open WebUI builds: url (the Endpoint field), api_key, timeout.
    return QdrantClient(url=server["base"] + "/qdrant", api_key=key or server["key"], timeout=30)


def _query(client, text, **kw):
    deadline = time.time() + 30
    while True:  # the embeddings are written behind the add
        resp = client.query_points(collection_name=COLLECTION, query=embed(text), using=None, limit=3, **kw)
        if len(resp.points) == len(DOCS) or time.time() > deadline:
            return resp
        time.sleep(0.3)


def test_version_probe_is_accepted(server):
    # The check QdrantClient runs in a thread at start-up (it only warns).
    remote = _client(server)._client
    version = get_server_version(remote.rest_uri, remote._rest_headers, None, 10)
    assert is_compatible(importlib.metadata.version("qdrant-client"), version)


def test_query_points_with_open_webui_defaults(server):
    resp = _query(_client(server), "owl at night")
    assert len(resp.points) == 3
    top = resp.points[0].model_dump()
    # Open WebUI's mapping: content payload.text, metadata payload.metadata, id, score.
    assert top["payload"]["text"] == DOCS["owl"]
    assert top["payload"]["metadata"]["key"] == "owl"
    assert top["payload"]["metadata"]["source"] == "owl"
    assert top["payload"]["metadata"]["name"] == "Owl"
    assert top["payload"]["metadata"]["collection"] == COLLECTION
    assert len(str(top["id"])) == 36
    assert resp.points[0].score > resp.points[1].score


def test_dimension_mismatch_names_both(server):
    with pytest.raises(UnexpectedResponse) as err:
        _client(server).query_points(collection_name=COLLECTION, query=[0.1, 0.2, 0.3], limit=1)
    assert err.value.status_code == 400
    assert "3 dimensions" in str(err.value) and f"({DIMS} dimensions)" in str(err.value)


def test_named_vector_is_refused(server):
    with pytest.raises(UnexpectedResponse) as err:
        _client(server).query_points(collection_name=COLLECTION, query=embed("owl"), using="dense", limit=1)
    assert err.value.status_code == 400
    assert "one unnamed vector" in str(err.value)


def test_missing_collection_and_bad_key(server):
    with pytest.raises(UnexpectedResponse) as err:
        _client(server).query_points(collection_name="nope", query=embed("owl"), limit=1)
    assert err.value.status_code == 404
    with pytest.raises(UnexpectedResponse) as err:
        _client(server, key="mddb_wrong").query_points(collection_name=COLLECTION, query=embed("owl"), limit=1)
    assert err.value.status_code == 401
