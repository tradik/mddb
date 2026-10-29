# Mailbox

1. 2026-10-26T08:00:00Z — Alice <alice@example.org> — Standup moved
2. 2026-10-26T09:30:00Z — Bob <bob@example.org> — Release notes
--- format=mbox converted=true confidence=1.00

=== part /message-1 (parent "")
# Standup moved

- **From:** Alice <alice@example.org>
- **To:** team@example.org
- **Date:** 2026-10-26T08:00:00Z

Standup moves to 10:00 in the café.
From now on, bring coffee.
--- format=eml converted=true confidence=1.00
--- meta date=["2026-10-26T08:00:00Z"]
--- meta from=["Alice <alice@example.org>"]
--- meta messageId=["standup@example.org"]
--- meta subject=["Standup moved"]
--- meta to=["team@example.org"]

=== part /message-2 (parent "")
# Release notes

- **From:** Bob <bob@example.org>
- **To:** team@example.org
- **Date:** 2026-10-26T09:30:00Z

## Release 4.2

- Faster sync
- Dark mode
--- format=eml converted=true confidence=1.00
--- meta date=["2026-10-26T09:30:00Z"]
--- meta from=["Bob <bob@example.org>"]
--- meta subject=["Release notes"]
--- meta to=["team@example.org"]
