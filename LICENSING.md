# Licensing

MDDB is source-available. The product is licensed under the Business Source
License 1.1; the libraries you link into your own software are licensed under
the BSD 3-Clause License. The nearest `LICENSE` file above a source file is the
one that applies to it. `services/mddbd/license_map_doc_test.go` checks that
every package manifest in the repository declares the licence this table gives
its directory.

## Which licence applies where

| Directory | SPDX | LICENSE file | Covers |
|---|---|---|---|
| `/` | BUSL-1.1 | `LICENSE` | the server `mddbd`, `mddb-cli`, the web panel, the chat service, scripts, tests, documentation and everything not listed below |
| `clients/` | BSD-3-Clause | `clients/LICENSE` | the Go, Node.js and Python client libraries |
| `integrations/` | BSD-3-Clause | `integrations/LICENSE` | the Airbyte destination, the Chrome extension, the GitHub Action, the Grafana data source, the LangChain adapter and the WordPress plugin |
| `proto/` | BSD-3-Clause | `proto/LICENSE` | the gRPC and Protobuf definitions, so anyone can generate a client |
| `services/php-extension/` | BSD-3-Clause | `services/php-extension/LICENSE` | the PHP client extension |
| `services/python-extension/` | BSD-3-Clause | `services/python-extension/LICENSE` | the Python client extension |
| `services/mddb-chat-widget/` | BSD-3-Clause | `services/mddb-chat-widget/LICENSE` | the embeddable chat widget |

Releases up to and including 2.18.11 were published under the BSD 3-Clause
License and stay under it. The Business Source License applies from 2.19.0.

## What the Business Source License means for you

**Running MDDB inside your organisation is free.** Production use is allowed
by the Additional Use Grant in `LICENSE`: install it, store your data in it,
connect your agents to it, serve your own websites and applications from it,
modify it, keep the changes to yourself. Your affiliates under common control
count as your organisation.

**Offering MDDB to third parties as a product needs a commercial licence.**
The one thing the grant withholds is selling MDDB itself: a hosted MDDB
service, a paid product that embeds MDDB or needs MDDB downloaded to work, or
paid support for MDDB that competes with Tradik Limited's own. A free product
is never competitive under the grant. If you are unsure which side your plan
falls on, ask before you build.

**Every version becomes Apache 2.0 after four years.** The Change Date is four
years from the day a version is published. From that day the version is under
the Apache License, Version 2.0, with no restrictions on production use.

**The BSD parts stay BSD.** Client libraries, integrations and the protocol
definitions are meant to be copied into your codebase, published in your app
store listings and vendored by your build. The Business Source License does
not reach them.

## Contributing

Contributions to the directories under BUSL-1.1 are accepted under the terms
in [CONTRIBUTING.md](CONTRIBUTING.md): you keep your copyright and grant
Tradik Limited the right to license your contribution under the Business
Source License, the Change License and commercial licences. Contributions to
the BSD directories are licensed under BSD-3-Clause like the code around them.

## Commercial licences and support

Tradik Limited offers commercial licences for competitive offerings and
support agreements for everyone else. Contact Tradik Limited through
<https://tradik.com> or open a discussion in the repository.

## Third-party code

The licences of MDDB's dependencies are listed in
[docs/LICENSE_AUDIT.md](docs/LICENSE_AUDIT.md). None of them is a copyleft
licence that would reach MDDB's own code; two libraries are MPL-2.0, which
covers their own files only.
