"""LangChain integration for MDDB (INT-017).

    from langchain_mddb import MddbVectorStore

    store = MddbVectorStore(collection="docs", address="localhost:11024")
    store.add_texts(["the first document"], [{"tag": "example"}])

    for doc in store.similarity_search("first", k=3):
        print(doc.metadata["key"], doc.page_content[:60])

    retriever = store.as_retriever(search_kwargs={"k": 5})

Deep Agents filesystem backend (INT-021), with the ``deepagents`` extra:

    from langchain_mddb import MddbBackend

    backend = MddbBackend(collection="agent-memory", address="localhost:11024")

LangGraph store (INT-020), with the ``store`` extra:

    from langchain_mddb import MddbStore

    store = MddbStore(collection="langgraph_store", address="localhost:11024")
"""

from typing import Any

from .vectorstore import MddbVectorStore

__version__ = "2.12.0"
__all__ = ["MddbBackend", "MddbStore", "MddbVectorStore"]


def __getattr__(name: str) -> Any:
    # Imported on first use, so the vector store works without deepagents
    # or langgraph installed.
    if name == "MddbBackend":
        from .backend import MddbBackend

        return MddbBackend
    if name == "MddbStore":
        from .store import MddbStore

        return MddbStore
    raise AttributeError(name)
