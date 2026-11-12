These are the collections the assistant may search, and how each one is kept:

| Collection | Embeddings | Retention | Backed up | Replicated |
|------------|------------|-----------|-----------|------------|
| docs       | enabled    | 30 days   | yes       | yes        |
| guides     | enabled    | 30 days   | yes       | yes        |
| runbooks   | enabled    | 30 days   | yes       | yes        |
| faq        | enabled    | 30 days   | yes       | yes        |
| changelog  | enabled    | 30 days   | yes       | yes        |
| api        | enabled    | 30 days   | yes       | yes        |
| releases   | enabled    | 30 days   | yes       | yes        |
| blog       | enabled    | 30 days   | yes       | yes        |
| support    | enabled    | 30 days   | yes       | yes        |
| internal   | enabled    | 30 days   | yes       | yes        |
  | archive    | enabled    | 30 days   | yes       | yes        |
  | archive    | enabled    | 30 days   | yes       | yes        |

All of them use the same settings, so a change to the defaults in `mddbd` reaches every collection at once. The only exception would be a collection with its own retrieval profile, and none of these has one.
