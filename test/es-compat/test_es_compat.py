"""elasticsearch-py against MDDB's Elasticsearch-compatible subset (INT-019).

The acceptance test of the ticket: the official client, unmodified, indexes,
bulk-loads, searches (match, bool, knn), counts and deletes documents in MDDB.

The suite starts the server itself from MDDB_BIN (a built mddbd), with
MDDB_ES_COMPAT=true and the lexical embedding provider, so knn works with no
model and no network. With MDDB_ES_URL set it uses that server instead.
"""

import os
import socket
import subprocess
import tempfile
import time
import urllib.request

import pytest
from elasticsearch import BadRequestError, Elasticsearch, NotFoundError, helpers

INDEX = "books"


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_healthy(base, proc):
    deadline = time.time() + 60
    while time.time() < deadline:
        if proc is not None and proc.poll() is not None:
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
def es():
    url = os.environ.get("MDDB_ES_URL")
    proc = None
    if not url:
        binary = os.environ.get("MDDB_BIN")
        if not binary:
            pytest.skip("set MDDB_BIN (a built mddbd) or MDDB_ES_URL")
        port = _free_port()
        data = tempfile.mkdtemp(prefix="mddb-es-")
        env = dict(os.environ, MDDB_ES_COMPAT="true", MDDB_EMBEDDING_PROVIDER="lexical",
                   MDDB_PATH=os.path.join(data, "mddb.db"), MDDB_HTTP_ADDR=f"127.0.0.1:{port}",
                   MDDB_GRPC_ENABLED="false", MDDB_MCP_ENABLED="false", MDDB_HTTP3_ENABLED="false")
        proc = subprocess.Popen([binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        url = f"http://127.0.0.1:{port}/es"
    _wait_healthy(url.rsplit("/es", 1)[0], proc)
    client = Elasticsearch(url)
    yield client
    client.close()
    if proc is not None:
        proc.terminate()
        proc.wait(timeout=30)


@pytest.fixture(scope="session")
def loaded(es):
    es.index(index=INDEX, id="fox", document={"content": "The quick brown fox jumps over the lazy dog", "genre": "fable"})
    ok, errors = helpers.bulk(es, [
        {"_index": INDEX, "_id": "bear", "content": "Brown bears eat honey in the forest", "genre": "nature"},
        {"_index": INDEX, "_id": "turtle", "content": "A slow green turtle crosses the road", "genre": "fable"},
        {"_index": INDEX, "_id": "owl", "content": "The owl hunts mice at night", "genre": "nature", "year": 2021},
    ], refresh=True)
    assert ok == 3 and not errors
    es.indices.refresh(index=INDEX)
    return es


def _ids(resp):
    return [h["_id"] for h in resp["hits"]["hits"]]


def test_info_and_ping(es):
    info = es.info()
    assert info["version"]["number"].startswith("8.")
    assert "not Elasticsearch" in info["tagline"]
    assert es.ping()


def test_get(loaded):
    doc = loaded.get(index=INDEX, id="fox")
    assert doc["found"] and doc["_source"]["genre"] == "fable"
    with pytest.raises(NotFoundError):
        loaded.get(index=INDEX, id="missing")


def test_match(loaded):
    resp = loaded.search(index=INDEX, query={"match": {"content": "brown"}}, highlight={"fields": {"content": {}}})
    assert sorted(_ids(resp)) == ["bear", "fox"]
    assert resp["hits"]["total"]["value"] == 2
    assert all("<em>" in h["highlight"]["content"][0] for h in resp["hits"]["hits"])


def test_bool(loaded):
    resp = loaded.search(index=INDEX, query={"bool": {
        "must": [{"match": {"content": "brown"}}],
        "filter": [{"term": {"genre": "nature"}}],
    }})
    assert _ids(resp) == ["bear"]
    resp = loaded.search(index=INDEX, query={"bool": {
        "should": [{"match": {"content": "owl"}}, {"match": {"content": "turtle"}}],
        "must_not": [{"term": {"genre": "fable"}}],
    }})
    assert _ids(resp) == ["owl"]


def test_knn(loaded):
    resp = loaded.search(index=INDEX, knn={
        "field": "embedding", "k": 2,
        "query_vector_builder": {"text_embedding": {"model_text": "brown fox jumps"}},
    })
    assert _ids(resp)[0] == "fox" and len(_ids(resp)) == 2


def test_aggs_and_count(loaded):
    resp = loaded.search(index=INDEX, size=0, aggs={"genres": {"terms": {"field": "genre"}}})
    buckets = {b["key"]: b["doc_count"] for b in resp["aggregations"]["genres"]["buckets"]}
    assert buckets == {"fable": 2, "nature": 2}
    assert loaded.count(index=INDEX)["count"] == 4
    assert loaded.count(index=INDEX, query={"term": {"genre": "nature"}})["count"] == 2


def test_unsupported_clause_is_refused(loaded):
    with pytest.raises(BadRequestError) as e:
        loaded.search(index=INDEX, query={"fuzzy": {"content": "brwn"}})
    assert "fuzzy" in str(e.value.body)


def test_delete(loaded):
    assert loaded.delete(index=INDEX, id="turtle")["result"] == "deleted"
    with pytest.raises(NotFoundError):
        loaded.delete(index=INDEX, id="turtle")
    ok, _ = helpers.bulk(loaded, [{"_op_type": "delete", "_index": INDEX, "_id": "owl"}], refresh=True)
    assert ok == 1
    assert loaded.count(index=INDEX)["count"] == 2
