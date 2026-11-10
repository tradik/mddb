"""
title: MDDB RAG Model
author: MDDB
version: 2.11.4
license: BUSL-1.1
description: RAG model using MDDB for document retrieval with multi-LLM support (Ollama, OpenAI, DeepSeek)
"""

import json
import requests
from typing import Generator, Iterator, Union, List, Dict
from pydantic import BaseModel, Field


class Pipe:
    """MDDB RAG Model - retrieves documents from MDDB and answers using configurable LLM."""

    class Valves(BaseModel):
        mddbUrl: str = Field(
            default="http://mddb:9000",
            description="MDDB MCP server URL (default port 9000)"
        )
        collection: str = Field(
            default="docs",
            description="Default MDDB collection"
        )
        topK: int = Field(
            default=5,
            description="Number of documents to retrieve"
        )
        threshold: float = Field(
            default=0.5,
            description="Minimum similarity threshold for semantic search"
        )
        llmProvider: str = Field(
            default="ollama",
            description="LLM provider: ollama, openai, or deepseek"
        )
        llmModel: str = Field(
            default="llama3.2:latest",
            description="Model name (e.g. llama3.2:latest, gpt-4o, deepseek-chat)"
        )
        llmApiUrl: str = Field(
            default="http://ollama:11434",
            description="LLM API URL"
        )
        llmApiKey: str = Field(
            default="",
            description="API key for OpenAI/DeepSeek (not needed for Ollama)"
        )

    def __init__(self):
        self.type = "manifold"
        self.valves = self.Valves()

        self.systemPrompt = """You are a helpful assistant.
Answer questions using ONLY the context provided from the knowledge base.
If the context doesn't contain enough information to answer, say so.
Always be helpful, accurate, and provide specific details when available.
Include relevant links and sources when mentioning specific topics."""

    def pipes(self):
        """Return available model configurations."""
        return [
            {"id": "mddb-rag", "name": "MDDB RAG Assistant"},
        ]

    def _vectorSearch(self, query: str) -> List[Dict]:
        """Direct vector search via MDDB REST API."""
        try:
            response = requests.post(
                f"{self.valves.mddbUrl}/v1/vector-search",
                json={
                    "collection": self.valves.collection,
                    "query": query,
                    "topK": self.valves.topK,
                    "threshold": self.valves.threshold,
                    "includeContent": True
                },
                timeout=10
            )
            if response.status_code == 200:
                return response.json().get("results", [])
        except Exception:
            pass
        return []

    def _fullTextSearch(self, query: str) -> List[Dict]:
        """Full-text search via MCP endpoint."""
        try:
            response = requests.post(
                f"{self.valves.mddbUrl}/tools/call",
                json={
                    "name": "full_text_search",
                    "arguments": {
                        "collection": self.valves.collection,
                        "query": query,
                        "limit": self.valves.topK,
                        "algorithm": "bm25"
                    }
                },
                timeout=10
            )
            if response.status_code == 200:
                data = response.json()
                return data.get("content", []) if isinstance(data, dict) else []
        except Exception:
            pass
        return []

    def _formatContext(self, results: List[Dict]) -> str:
        """Format search results as context for LLM."""
        if not results:
            return "No relevant documents found in the knowledge base."

        context_parts = []
        for i, doc in enumerate(results[:self.valves.topK], 1):
            key = doc.get("key", doc.get("id", "unknown"))
            content = doc.get("contentMd", doc.get("content", ""))
            score = doc.get("score", doc.get("similarity", 0))
            meta = doc.get("meta", {})
            title = meta.get("title", [key])[0] if isinstance(meta.get("title"), list) else meta.get("title", key)

            if len(content) > 2000:
                content = content[:2000] + "..."

            context_parts.append(f"## Document {i}: {title}\n**Score:** {score:.2f}\n**Key:** {key}\n\n{content}")

        return "\n\n---\n\n".join(context_parts)

    def pipe(self, body: dict) -> Union[str, Generator, Iterator]:
        """Process request with RAG."""

        messages = body.get("messages", [])

        # Get last user message
        lastUserMsg = ""
        for msg in reversed(messages):
            if msg.get("role") == "user":
                lastUserMsg = msg.get("content", "")
                break

        # Search for relevant documents
        results = []
        if lastUserMsg:
            results = self._vectorSearch(lastUserMsg)
            if not results:
                results = self._fullTextSearch(lastUserMsg)

        # Build context
        context = self._formatContext(results)

        # Build system prompt with context
        fullSystemPrompt = f"""{self.systemPrompt}

---
KNOWLEDGE BASE CONTEXT:

{context}

---
Use the context above to answer the user's question. Be specific and cite relevant information."""

        # Prepare messages with system prompt
        ragMessages = [{"role": "system", "content": fullSystemPrompt}]
        for msg in messages:
            if msg.get("role") != "system":
                ragMessages.append(msg)

        stream = body.get("stream", True)
        try:
            gen = self._callLLM(ragMessages, stream)
            if stream:
                return gen
            else:
                return "".join(gen)
        except Exception as e:
            return f"Error: {str(e)}\n\nPlease check that your LLM provider and MDDB services are running."

    def _callLLM(self, messages, stream=True):
        """Call LLM based on configured provider."""
        provider = self.valves.llmProvider.lower()
        if provider == "ollama":
            return self._callOllama(messages, stream)
        else:
            return self._callOpenAICompat(messages, stream)

    def _callOllama(self, messages, stream):
        """Call Ollama API."""
        payload = {
            "model": self.valves.llmModel,
            "messages": messages,
            "stream": stream,
        }

        if stream:
            response = requests.post(
                f"{self.valves.llmApiUrl}/api/chat",
                json=payload,
                stream=True,
                timeout=120
            )
            response.raise_for_status()

            for line in response.iter_lines():
                if line:
                    try:
                        data = json.loads(line)
                        if "message" in data and "content" in data["message"]:
                            yield data["message"]["content"]
                        if data.get("done", False):
                            break
                    except json.JSONDecodeError:
                        continue
        else:
            payload["stream"] = False
            response = requests.post(
                f"{self.valves.llmApiUrl}/api/chat",
                json=payload,
                timeout=120
            )
            response.raise_for_status()
            yield response.json().get("message", {}).get("content", "")

    def _callOpenAICompat(self, messages, stream):
        """Call OpenAI-compatible API (OpenAI, DeepSeek, etc.)."""
        headers = {"Content-Type": "application/json"}
        if self.valves.llmApiKey:
            headers["Authorization"] = f"Bearer {self.valves.llmApiKey}"

        payload = {
            "model": self.valves.llmModel,
            "messages": messages,
            "stream": stream,
        }

        if stream:
            response = requests.post(
                f"{self.valves.llmApiUrl}/v1/chat/completions",
                headers=headers,
                json=payload,
                stream=True,
                timeout=120
            )
            response.raise_for_status()

            for line in response.iter_lines():
                if line:
                    line = line.decode("utf-8") if isinstance(line, bytes) else line
                    if line.startswith("data: "):
                        line = line[6:]
                    if line == "[DONE]":
                        break
                    try:
                        data = json.loads(line)
                        delta = data.get("choices", [{}])[0].get("delta", {})
                        if "content" in delta:
                            yield delta["content"]
                    except json.JSONDecodeError:
                        continue
        else:
            payload["stream"] = False
            response = requests.post(
                f"{self.valves.llmApiUrl}/v1/chat/completions",
                headers=headers,
                json=payload,
                timeout=120
            )
            response.raise_for_status()
            yield response.json()["choices"][0]["message"]["content"]
