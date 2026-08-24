"""LangChain integration for MDDB (INT-017).

    from langchain_mddb import MddbVectorStore

    store = MddbVectorStore(collection="docs", address="localhost:11024")
    store.add_texts(["the first document"], [{"tag": "example"}])

    for doc in store.similarity_search("first", k=3):
        print(doc.metadata["key"], doc.page_content[:60])

    retriever = store.as_retriever(search_kwargs={"k": 5})
"""

from .vectorstore import MddbVectorStore

__version__ = "2.12.0"
__all__ = ["MddbVectorStore"]
