#!/usr/bin/env python3
"""Reference encoder for semantic code facts v1; writes cases.json.

The format is described in README.md. mddbd (validation) and mddb-cli
(encoding) implement it separately and both run cases.json, so a change to
the format starts here: edit, run `python3 generate.py`, then update both
implementations until their tests pass again.
"""

import hashlib
import json
import os
import re

MAX_LINES = 64
MAX_VALUE = 1024
DEFAULT_MAX_FACTS = 4096
KINDS = {"function", "method", "class", "interface", "struct", "type", "enum", "constructor"}
NAME_RE = re.compile(r"^[^\s|#]+$")
PATH_RE = re.compile(r"^[^\s|]+$")


def h12(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()[:12]


def encode(content, source, facts):
    """What the CLI sends: facts -> meta."""
    defs = []
    for d in facts.get("defs", []):
        v = f'{d["name"]} {d["kind"]} {d["start"]}-{d["end"]}'
        if d.get("container"):
            v += " " + d["container"]
        defs.append(v)
    refs = []
    for r in facts.get("refs", []):
        lines = sorted(set(r["lines"]))
        for i in range(0, len(lines), MAX_LINES):
            refs.append(r["target"] + " " + ",".join(str(n) for n in lines[i:i + MAX_LINES]))
    impls = [f'{i["target"]} {i["local"]}' for i in facts.get("impls", [])]
    out = {"symDefs": sorted(defs), "symRefs": sorted(refs), "symImpls": sorted(impls)}
    out = {k: v for k, v in out.items() if v}
    out["symIndex"] = [f"v1 {h12(content)} {digest(out)} {source}"]
    return out


def digest(meta):
    buf = ""
    for key in ("symDefs", "symRefs", "symImpls"):
        for v in sorted(meta.get(key, [])):
            buf += key + "\t" + v + "\n"
    return h12(buf)


def valid(key, v):
    if len(v.encode("utf-8")) > MAX_VALUE or "|" in v:
        return False
    parts = v.split(" ")
    if key == "symDefs":
        if len(parts) not in (3, 4) or not NAME_RE.match(parts[0]) or parts[1] not in KINDS:
            return False
        m = re.fullmatch(r"([1-9][0-9]*)-([1-9][0-9]*)", parts[2])
        if not m or int(m.group(1)) > int(m.group(2)):
            return False
        return len(parts) == 3 or bool(NAME_RE.match(parts[3]))
    if len(parts) != 2 or "#" not in parts[0]:
        return False
    path, name = parts[0].rsplit("#", 1)
    if not PATH_RE.match(path) or not NAME_RE.match(name):
        return False
    if key == "symRefs":
        nums = parts[1].split(",")
        if not 1 <= len(nums) <= MAX_LINES or not all(re.fullmatch(r"[1-9][0-9]*", n) for n in nums):
            return False
        ints = [int(n) for n in nums]
        return ints == sorted(set(ints))
    return bool(NAME_RE.match(parts[1]))  # symImpls


def keep(content, is_code, sent, max_facts):
    """What the server stores from the sym* keys it received."""
    if not is_code:
        return {}
    idx = sent.get("symIndex", [])
    if len(idx) != 1:
        return {}
    parts = idx[0].split(" ")
    if len(parts) != 4 or parts[0] != "v1" or parts[1] != h12(content) or not NAME_RE.match(parts[3]):
        return {}
    out = {}
    budget = max_facts
    truncated = False
    for key in ("symDefs", "symImpls", "symRefs"):
        vals = sorted(v for v in sent.get(key, []) if valid(key, v))
        if len(vals) > budget:
            vals, truncated = vals[:budget], True
        budget -= len(vals)
        if vals:
            out[key] = vals
    out["symIndex"] = [idx[0] + (" truncated" if truncated else "")]
    return out


GO_CONTENT = """package store

type Closer interface {
\tClose() error
}

type File struct{ name string }

func (f *File) Close() error {
\treturn flush(f.name)
}

func flush(name string) error { return nil }
"""

PY_CONTENT = """class Outer:
    class Inner:
        def method(self):
            return helper()

def helper():
    return 1
"""

PHP_CONTENT = """<?php
namespace App\\Http;

interface Handler { public function handle(): void; }

class Kernel implements Handler {
    public function handle(): void { $this->boot(); }
    private function boot(): void {}
}
"""

CASES = [
    {
        "name": "go: method, interface and implementation",
        "key": "store/file.go", "path": "store/file.go", "language": "go",
        "content": GO_CONTENT, "source": "gopls/0.24.0",
        "facts": {
            "defs": [
                {"name": "Closer", "kind": "interface", "start": 3, "end": 5},
                {"name": "File", "kind": "struct", "start": 7, "end": 7},
                {"name": "File.Close", "kind": "method", "start": 9, "end": 11, "container": "File"},
                {"name": "flush", "kind": "function", "start": 13, "end": 13},
            ],
            "refs": [{"target": "store/file.go#flush", "lines": [10]},
                     {"target": "store/file.go#File", "lines": [9]}],
            "impls": [{"target": "store/file.go#Closer", "local": "File"}],
        },
    },
    {
        "name": "python: nested class, qualified names",
        "key": "pkg/outer.py", "path": "pkg/outer.py", "language": "python",
        "content": PY_CONTENT, "source": "pyrefly/1.4.1",
        "facts": {
            "defs": [
                {"name": "Outer", "kind": "class", "start": 1, "end": 4},
                {"name": "Outer.Inner", "kind": "class", "start": 2, "end": 4, "container": "Outer"},
                {"name": "Outer.Inner.method", "kind": "method", "start": 3, "end": 4, "container": "Outer.Inner"},
                {"name": "helper", "kind": "function", "start": 6, "end": 7},
            ],
            "refs": [{"target": "pkg/outer.py#helper", "lines": [4]}],
        },
    },
    {
        "name": "php: namespaced names keep their backslashes",
        "key": "src/Http/Kernel.php", "path": "src/Http/Kernel.php", "language": "php",
        "content": PHP_CONTENT, "source": "phpactor/2026.10.07.0",
        "facts": {
            "defs": [
                {"name": "App\\Http\\Handler", "kind": "interface", "start": 4, "end": 4},
                {"name": "App\\Http\\Kernel", "kind": "class", "start": 6, "end": 9},
                {"name": "App\\Http\\Kernel.handle", "kind": "method", "start": 7, "end": 7, "container": "App\\Http\\Kernel"},
                {"name": "App\\Http\\Kernel.boot", "kind": "method", "start": 8, "end": 8, "container": "App\\Http\\Kernel"},
            ],
            "refs": [{"target": "src/Http/Kernel.php#App\\Http\\Kernel.boot", "lines": [7]},
                     {"target": "src/Http/Kernel.php#App\\Http\\Handler", "lines": [6]}],
            "impls": [{"target": "src/Http/Kernel.php#App\\Http\\Handler", "local": "App\\Http\\Kernel"}],
        },
    },
    {
        "name": "more than 64 lines for one symbol continue in a second value",
        "key": "big.go", "path": "big.go", "language": "go",
        "content": "package big\n", "source": "gopls/0.24.0",
        "facts": {"refs": [{"target": "util.go#log", "lines": list(range(70, 0, -1)) + [5]}]},
    },
    {
        "name": "a path may contain # (the name follows the last #)",
        "key": "c#/x.go", "path": "c#/x.go", "language": "go",
        "content": "package x\n", "source": "gopls/0.24.0",
        "facts": {"refs": [{"target": "c#/y.go#Run", "lines": [1]}]},
    },
    {
        "name": "a file with nothing to say keeps symIndex alone",
        "key": "empty.go", "path": "empty.go", "language": "go",
        "content": "package empty\n", "source": "gopls/0.24.0",
        "facts": {},
    },
    {
        "name": "over the limit: definitions first, then implementations, then references",
        "key": "store/file.go", "path": "store/file.go", "language": "go",
        "content": GO_CONTENT, "source": "gopls/0.24.0", "maxFacts": 5,
        "facts": {
            "defs": [
                {"name": "Closer", "kind": "interface", "start": 3, "end": 5},
                {"name": "File", "kind": "struct", "start": 7, "end": 7},
                {"name": "File.Close", "kind": "method", "start": 9, "end": 11, "container": "File"},
                {"name": "flush", "kind": "function", "start": 13, "end": 13},
            ],
            "refs": [{"target": "store/file.go#flush", "lines": [10]},
                     {"target": "store/file.go#File", "lines": [9]}],
            "impls": [{"target": "store/file.go#Closer", "local": "File"}],
        },
    },
    {
        "name": "content changed after the facts were computed: all removed",
        "key": "store/file.go", "path": "store/file.go", "language": "go",
        "content": GO_CONTENT, "storedContent": GO_CONTENT + "// edited\n", "source": "gopls/0.24.0",
        "facts": {"defs": [{"name": "flush", "kind": "function", "start": 13, "end": 13}]},
    },
    {
        "name": "not code: sym keys are not kept",
        "key": "notes.md", "path": "notes.md", "language": "",
        "content": "# Notes\n", "source": "gopls/0.24.0", "isCode": False,
        "facts": {"defs": [{"name": "Notes", "kind": "type", "start": 1, "end": 1}]},
    },
    {
        "name": "server only: malformed values are dropped, the rest kept",
        "key": "a.go", "path": "a.go", "language": "go",
        "content": "package a\n",
        "sent": {
            "symIndex": [f"v1 {h12('package a' + chr(10))} 000000000000 gopls/0.24.0"],
            "symDefs": [
                "Good function 1-1",
                "Bad|Pipe function 1-1",
                "Unknown widget 1-1",
                "Backwards function 5-2",
                "Zero function 0-1",
                "Too many fields function 1-1 X Y",
            ],
            "symRefs": [
                "b.go#Run 1,2",
                "b.go#Run 2,1",
                "b.go#Run 1,1",
                "noHash 3",
                "b.go#Run x",
                "b.go#Run " + ",".join(str(n) for n in range(1, 66)),
            ],
            "symImpls": ["i.go#Iface Local", "i.go#Iface", "i.go#Iface Lo cal"],
        },
    },
    {
        "name": "server only: two symIndex values or another version remove everything",
        "key": "a.go", "path": "a.go", "language": "go",
        "content": "package a\n",
        "sent": {
            "symIndex": [f"v2 {h12('package a' + chr(10))} 000000000000 gopls/0.24.0"],
            "symDefs": ["Good function 1-1"],
        },
    },
]


def build():
    out = []
    for c in CASES:
        case = {k: v for k, v in c.items() if k not in ("sent",)}
        case.setdefault("isCode", True)
        case.setdefault("maxFacts", DEFAULT_MAX_FACTS)
        if "facts" in c:
            case["encoded"] = encode(c["content"], c["source"], c["facts"])
            sent = case["encoded"]
        else:
            sent = c["sent"]
            case["sent"] = sent
        stored_content = c.get("storedContent", c["content"])
        case["stored"] = keep(stored_content, case["isCode"], sent, case["maxFacts"])
        out.append(case)
    return out


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "cases.json"), "w", encoding="utf-8") as f:
        json.dump(build(), f, indent=2, ensure_ascii=False)
        f.write("\n")
