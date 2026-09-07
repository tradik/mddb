---
title: "FTS Spell Correction"
slug: "docs/symspell"
description: "SymSpell spell correction for MDDB full-text search: symmetric delete lookup, per-collection dictionaries, and when a query is corrected at all."
status: publish
---

# FTS Spell Correction

MDDB includes a SymSpell-based spell checker that improves full-text search recall by correcting typos before querying the FTS index.

## Enable

Spell correction is **disabled by default**. Enable it via environment variable:

```bash
MDDB_SPELL=true mddbd
```

Or with Docker:

```yaml
environment:
  MDDB_SPELL: "true"
```

When enabled, MDDB builds a frequency dictionary from all indexed documents at startup. The index loads in the background — queries return `503` until ready.

## Endpoints

### Spell Suggestions

`POST /v1/spell-suggest`

Returns per-token suggestions and a corrected version of the input text.

```json
{
  "collection": "articles",
  "text": "quikc brwon fox",
  "lang": "en",
  "maxSuggestions": 3
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `text` | ✓ | Input text to check |
| `lang` | ✓ | Language code (`en`, `pl`, `de`, …) |
| `collection` | — | Scope to collection's custom dictionary |
| `maxSuggestions` | — | Max suggestions per token (default 5) |

**Response:**

```json
{
  "originalText": "quikc brwon fox",
  "suggestedText": "quick brown fox",
  "tokenSuggestions": [
    { "token": "quikc", "suggestions": ["quick"] },
    { "token": "brwon", "suggestions": ["brown"] }
  ]
}
```

### Apply Corrections

`POST /v1/spell-cleanup`

Applies the best correction for each token and returns cleaned text.

```json
{
  "collection": "articles",
  "text": "quikc brwon fox",
  "lang": "en"
}
```

**Response:**

```json
{
  "original": "quikc brwon fox",
  "cleaned": "quick brown fox",
  "correctionsApplied": 2
}
```

### Custom Dictionary

#### List — `GET /v1/spell-dictionary?lang=en&collection=articles`

#### Add — `PUT /v1/spell-dictionary`

```json
{
  "collection": "articles",
  "lang": "en",
  "words": ["MDDB", "mddbd", "SymSpell"],
  "frequency": 500
}
```

Higher `frequency` gives words stronger priority. Default is `100`.

#### Remove — `DELETE /v1/spell-dictionary`

```json
{
  "collection": "articles",
  "lang": "en",
  "words": ["mddbd"]
}
```

## Integration with FTS

```bash
CLEANED=$(curl -s -X POST http://localhost:11023/v1/spell-cleanup \
  -d '{"text":"documnt retriveal","lang":"en"}' | jq -r .cleaned)

curl -s -X POST http://localhost:11023/v1/search \
  -d "{\"collection\":\"articles\",\"query\":\"$CLEANED\",\"lang\":\"en\"}"
```
