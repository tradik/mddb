---
title: "YouTube Transcript Analyzer"
slug: "docs/uses-youtube-transcribe"
description: "Load a YouTube channel's transcripts into MDDB, then ask questions about the channel's content with Claude CLI over semantic search."
status: publish
---

# YouTube Transcript Analyzer

Scan transcripts from a YouTube channel, load them into MDDB, then ask questions about the channel's content using Claude CLI.

```
YouTube → yt-dlp (transcripts) → MDDB → Claude CLI (MCP) → answers
```

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — download transcripts
- [MDDB](https://github.com/tradik/mddb) — document database
- [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) — question interface
- Python 3 (optional, for format conversion)

---

## Step 1: Install yt-dlp

```bash
# macOS
brew install yt-dlp

# Linux
pip install yt-dlp

# Windows
winget install yt-dlp
```

Verify the installation:

```bash
yt-dlp --version
```

---

## Step 2: Start MDDB

The simplest way — Docker:

```bash
docker run -d \
  --name mddb \
  -p 11023:11023 -p 9000:9000 \
  -v mddb-data:/app/data \
  tradik/mddb:latest
```

Check that it's running:

```bash
curl http://localhost:11023/v1/health
```

Expected response: a JSON object with `"status":"healthy"`.

---

## Step 3: Download transcripts from a YouTube channel

> **VPN recommended.** YouTube may throttle or block your IP when downloading many transcripts at once. Use a VPN to avoid rate limiting and potential IP bans.

### 3a. Get the list of videos

Replace `CHANNEL_URL` with the channel address, e.g. `https://www.youtube.com/@lexfridman`.

```bash
yt-dlp --flat-playlist --print "%(id)s %(title)s" "CHANNEL_URL" > video_list.txt
```

Check how many videos were found:

```bash
wc -l video_list.txt
```

### 3b. Download transcripts

Create a folder for transcripts:

```bash
mkdir -p transcripts
```

Download subtitles from each video:

```bash
while IFS=' ' read -r video_id title; do
  echo "Downloading: $title"
  yt-dlp \
    --write-subs \
    --write-auto-subs \
    --sub-lang "en" \
    --sub-format "vtt" \
    --skip-download \
    --output "transcripts/%(id)s" \
    "https://www.youtube.com/watch?v=$video_id" 2>/dev/null
done < video_list.txt
```

Options:
- `--sub-lang "en"` — download English subtitles (change to `"en,pl"` for multiple languages)
- `--write-auto-subs` — use auto-generated subs when manual ones are unavailable
- `--skip-download` — don't download the video itself, only subtitles

### 3c. Convert VTT to plain text

VTT transcripts contain timestamps. Convert them to clean Markdown:

```bash
mkdir -p transcripts_md

for vtt_file in transcripts/*.vtt; do
  base=$(basename "$vtt_file" .vtt)
  # Remove VTT headers, timestamps, and HTML tags
  sed '/^WEBVTT/d; /^$/d; /^[0-9][0-9]:[0-9][0-9]/d; /-->/d; s/<[^>]*>//g' \
    "$vtt_file" | \
    awk '!seen[$0]++' > "transcripts_md/${base}.md"
  echo "Converted: $base"
done
```

Verify the result:

```bash
ls transcripts_md/ | head -5
cat transcripts_md/$(ls transcripts_md/ | head -1) | head -20
```

---

## Step 4: Load transcripts into MDDB

### Option A: Use the load-md-folder.sh script

```bash
# Download the script (if you don't have the repository)
curl -O https://raw.githubusercontent.com/tradik/mddb/main/scripts/load-md-folder.sh
chmod +x load-md-folder.sh

# Load all transcripts into the "youtube" collection
./load-md-folder.sh transcripts_md/ youtube --lang en_US --verbose
```

### Option B: Use mddb-cli

Install the CLI from a release package (`.deb`, `.rpm` or a tarball; see
[INSTALLATION.md](INSTALLATION.md)), then load files
one by one. The key is the video ID, so the link to the video can go in the
metadata:

```bash
for md_file in transcripts_md/*.md; do
  key=$(basename "$md_file" .md)
  mddb-cli add youtube "$key" en_US -f "$md_file" \
    -m "source=youtube,type=transcript,url=https://www.youtube.com/watch?v=$key"
  echo "Loaded: $key"
done
```

### Option C: Use the HTTP API directly

```bash
for md_file in transcripts_md/*.md; do
  curl -s -X POST http://localhost:11023/v1/upload \
    -F "file=@$md_file" \
    -F "collection=youtube" \
    -F "lang=en_US"
  echo " -> $(basename "$md_file")"
done
```

Check how many documents were loaded:

```bash
curl -s http://localhost:11023/v1/stats | python3 -m json.tool
```

---

## Step 5: Configure Claude CLI (MCP)

Claude CLI connects to MDDB via the MCP protocol. The container started in
Step 2 serves it on port 9000 (Streamable HTTP at `/mcp`):

```bash
claude mcp add --transport http mddb http://localhost:9000/mcp
claude mcp list        # mddb should be listed as connected
```

Claude Desktop and other clients take the same URL; see [MCP.md](MCP.md). The
MCP port has no authentication unless you turn it on, so keep it on
`localhost` or set `MDDB_MCP_API_KEY_ENABLED=true`.

Without a running server, a local `mddbd` binary can serve MCP over stdio,
opening the database file itself (`MDDB_MCP_STDIO=true`,
`MDDB_PATH=/path/to/mddb.db`). It cannot open a file another `mddbd` already
has open, so use one or the other.

---

## Step 6: Ask questions

Launch Claude CLI:

```bash
claude
```

Now you can ask about the channel's content:

```
> What topics were most frequently discussed on this channel?

> Summarize the 5 most interesting episodes.

> Was there an episode about artificial intelligence? What was said?

> Find episodes where the guest talked about quantum physics.

> Compare the opinions of different guests on the future of AI.
```

Claude will automatically use MCP tools (`semantic_search`, `full_text_search`, `hybrid_search`) to search through transcripts and provide answers based on the actual content.

---

## Step 7 (optional): Enable vector search

Semantic search requires embeddings. Run Ollama locally:

```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Pull the embedding model
ollama pull nomic-embed-text
```

Configure MDDB to use Ollama (if running via Docker):

```bash
docker stop mddb
docker rm mddb
docker run -d \
  --name mddb \
  -p 11023:11023 -p 9000:9000 \
  -v mddb-data:/app/data \
  -e MDDB_EMBEDDING_PROVIDER=ollama \
  -e MDDB_EMBEDDING_API_URL=http://host.docker.internal:11434 \
  -e MDDB_EMBEDDING_MODEL=nomic-embed-text \
  -e MDDB_EMBEDDING_DIMENSIONS=768 \
  --add-host=host.docker.internal:host-gateway \
  tradik/mddb:latest
```

Reindex the collection:

```bash
curl -X POST http://localhost:11023/v1/vector-reindex \
  -H "Content-Type: application/json" -d '{"collection":"youtube"}'
```

Now `semantic_search` in Claude CLI will return semantically similar transcript fragments.

---

## Summary

| Step | Command | What it does |
|------|---------|--------------|
| 1 | `brew install yt-dlp` | Install download tool |
| 2 | `docker run tradik/mddb` | Start the database |
| 3 | `yt-dlp --write-subs` | Download transcripts |
| 4 | `load-md-folder.sh` | Load into MDDB |
| 5 | `claude mcp add --transport http mddb ...` | Configure MCP |
| 6 | `claude` | Ask questions |
| 7 | `ollama pull nomic-embed-text` | Add semantic search |
