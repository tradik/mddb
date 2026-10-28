---
title: "Why Put Your Code in a Database"
slug: "blog/why-put-your-code-in-a-database"
status: publish
type: post
date: 2026-10-28
tags: [rag, search, embeddings, mcp, agents]
excerpt: "Your repository already has grep. What storing code in MDDB adds: search by meaning, search by exact name and a graph of what depends on what, all reachable by an agent over MCP. Plus where it falls short."
description: "What storing and embedding source code in MDDB is good for: semantic code search, exact identifiers, a dependency graph and agent context over MCP."
---

Ask a coding assistant something about a repository it has never seen and
watch what it does. It greps, opens a file, greps again, opens three more,
and somewhere around the tenth file it either finds the answer or runs out
of context and guesses. On a small project that works. On a few thousand
files, or on your Terraform plus the Kubernetes manifests plus the Ansible
that deploys them, it gets slow and expensive, and it starts to make things
up.

The repository is not the problem. The problem is that grep answers one kind
of question and the assistant has three.

## Three questions, three indexes

Store a repository in MDDB one document per file and each file is indexed
three ways:

| You want to know | Index | Example |
|---|---|---|
| Where does something *like this* happen? | vectors | "where do we retry failed HTTP calls?" |
| Where is *exactly this* name? | full-text | `verifyDatabase`, `MDDB_BACKUP_DIR`, an error code |
| What depends on what? | `code_graph` | who calls this function, which Deployment mounts this Secret |

Each one is weak exactly where another is strong, so you want all three.

### Meaning: vector search

You often don't know the name of the thing you are looking for. You know
what it does. "Where do we rate-limit logins?" A text search for
`rate limit` finds the README and two comments. The function is called
`throttleAuth` and nothing in it says "rate".

An embedding of that function still lands near the question, because the
two are about the same thing. Finding code by what it does, when you don't
know what it's called, is what vectors are for.

It only works if the thing embedded is a whole thought. Code documents are
split for embedding where brackets balance, so a chunk is a complete function
rather than its second half and the first lines of the next one. A collection
that holds code should say so:

```bash
curl -X PUT "$MDDB/v1/collection-config" -H 'Content-Type: application/json' \
  -d '{"collection":"myproject","embedding":{"chunkMode":"code"}}'

curl -s -X POST "$MDDB/v1/vector-search" -H 'Content-Type: application/json' \
  -d '{"collection":"myproject","query":"where do we rate-limit logins","topK":5}'
```

### Names: full-text search

Vectors are bad at exact strings. An identifier, an environment variable, a
log message you just copied out of production: for those you want a
plain, exact match, and an embedding is a blurred approximation of one.

The full-text index treats code as code. `parse_request` is stored whole and
also as `parse` and `request`, nothing is stemmed, and keywords stay
searchable. When you want both at once, `/v1/hybrid-search` merges the two
rankings, so a result has to be about the right thing *and* mention the
right name to rise to the top.

### Structure: the code graph

The third question is the one people ask before they change something: what
breaks? Text search can't answer it, because finding a name is not the same
as depending on it. A comment that mentions a function is not a caller.

When a file is saved, MDDB reads what it defines, what it uses and what it
imports, then links files where one uses what another defines. Ask for a
document's neighbours and you get edges with a kind and the symbol behind
them:

```bash
# Who depends on this file?
curl -s "$MDDB/v1/code-graph?collection=myproject&key=src/auth/session.go&direction=in"

# Which manifests use the Secret named db?
curl -s "$MDDB/v1/fts?collection=infra&q=*&meta.uses=Secret/db"
```

This started with CSS, JavaScript and HTML in 2.12 and
[C in 2.18](https://mddb.tradik.com/blog/mddb-reads-c-code/). Since 2.18.6
it covers Go, Rust, Java, Python, Ruby and PHP, and also the files that
describe where the code runs:

- **Terraform:** `aws_instance.web`, `module.vpc` and `var.region` are
  defined in one file and referenced in another, written the way Terraform
  itself writes them.
- **Kubernetes and OpenShift:** a Deployment *uses* the ConfigMaps, Secrets,
  claims and service account it names; a Route or an Ingress uses its
  Service.
- **Kustomize:** an overlay imports its bases and patches.
- **Ansible:** a task that says `notify: Restart nginx` reaches the file with
  that handler, and `include_tasks` or a role leads to the file it loads.

Before this, finding out who used a Secret meant opening every manifest in
the repo.

## What it's good for

**Context for an agent.** This is the main reason. Over MCP, an assistant
gets `semantic_search`, `full_text_search`, `hybrid_search` and `code_graph`
as tools. It finds a few relevant chunks, follows the graph one step to see
what they touch, and reads only those files. That costs fewer tokens than
reading the tree, and the answer is grounded in files it can cite. All four
tools are read-only, so you can also expose them through
[custom tools](https://mddb.tradik.com/docs/custom-tools/) without handing out
write access.

**Impact before a change.** Before you rename a function or delete a
resource, ask the graph what points at it. The answer is a list of files and
the symbol that ties each one to yours, not forty grep hits to sort through
by hand.

**Finding the same bug twice.** You've just fixed a bug. Take the broken
snippet and run it as a vector query. Code written by the same person, or
copied from the same answer on the internet, tends to come back, often under
different names, which is why grep would have missed it.

**Spotting duplication.** When two functions in different packages have
embeddings that sit very close together, one of them may be redundant. Read
both before you merge them, though: code that looks alike can still differ in
an edge case that matters.

**Onboarding.** "How does a backup get written?" is a fair question on
someone's second day. With code and documentation in the same database, the
answer comes with the page that explains it and the code that does it. When
the two disagree, that shows up too.

**More than one repository.** A collection per repository, searched together
or apart, gives you one place to ask "who else calls this API?" across a
whole organisation. Tenant scoping keeps teams inside their own collections
when they need to be.

## Where it falls short

An agent that is confidently wrong costs you more than one that says it
doesn't know, so these are worth knowing before you rely on it.

- **The graph comes from a scanner, not a compiler.** Calls through a value
  or a package (`obj.Save()`, `pkg.Run()`) are left out, because from the
  text alone a method and a package function look the same. Function
  pointers aren't followed. Anything built at runtime is invisible. Treat an
  edge as a strong lead and the absence of one as "not proven".
- **Templated files are skipped.** A Helm chart does not parse as YAML until
  it is rendered, and a value like `{{ role_name }}` is not a path. Store
  rendered manifests if you want their graph.
- **The index is only as fresh as your last sync.** Symbols are extracted
  when a file is saved, so run the sync on every push (the
  [GitHub Action](https://github.com/tradik/mddb/tree/main/integrations/github-action)
  does it). Changing how a collection is chunked means embedding it again.
- **Embeddings cost something.** Time from the embedding model, and space on
  disk. Quantization cuts the space, and a collection you never search by
  text can turn full-text indexing off.
- **Whatever you store, an agent can read.** Keep `.env` files, keys and
  credentials out of the collection. Exclude them in the sync, not
  afterwards.

## Getting started

1. Load the repository, one document per file, with the file path as the key.
2. Set `embedding.chunkMode` to `code` on the collection, and configure an
   embedding provider.
3. Point your MCP client at the server.
4. Ask it something you'd normally grep for, then something you couldn't.

The extraction rules for each language are in the
[API reference](https://mddb.tradik.com/docs/api/), and the earlier post
[What Breaks If I Change This?](https://mddb.tradik.com/blog/what-breaks-if-i-change-this/)
walks through the graph on a web theme.
