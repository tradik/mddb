# MDDB Sync — WordPress plugin

[![PHP](https://img.shields.io/badge/PHP-%3E%3D8.2-777BB4?logo=php&logoColor=white)](https://www.php.net)
[![WordPress](https://img.shields.io/badge/WordPress-%3E%3D6.2-21759B?logo=wordpress&logoColor=white)](https://wordpress.org)
[![Coverage](https://img.shields.io/badge/coverage-%E2%89%A590%25-brightgreen)](#testing)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](#license)

Synchronises WordPress **posts** and **pages** (and any other public post type) to an [MDDB](https://github.com/tradik/mddb) instance — and, since 0.2.0, lets MDDB's MCP tools **publish back into WordPress**.

- **On save / publish** → `POST /v1/add` — upsert by stable key.
- **On trash / delete** → `POST /v1/delete` — remove the matching MDDB document.
- **Language detection** → reads from [Polylang](https://wordpress.org/plugins/polylang/), [WPML](https://wpml.org/), or falls back to the site locale.
- **Remote publishing (opt-in)** → `POST /wp-json/mddb-sync/v1/publish` and `/status` let the `wordpress_publish` / `wordpress_set_status` MCP tools create, update and (un)publish posts and pages — tags, categories, custom taxonomies, meta fields and Polylang/WPML translations included.
- **Self-updates** from this repository's GitHub Releases (no wp.org slug required).

The plugin is a thin, security-first PHP shim: settings UI, retry-aware HTTP client, hook wiring. No background queue, no JavaScript build step, no dashboard widgets.

---

## Install

### From a release zip (recommended)

1. Download `mddb-sync-<version>.zip` from the latest [GitHub Release](https://github.com/tradik/mddb/releases) (tag starts with `wp-v`).
2. In wp-admin: **Plugins → Add New → Upload Plugin** → choose the zip → **Install Now** → **Activate**.
3. Configure under **Settings → MDDB Sync**.

### From source (development)

```bash
cd integrations/wordpress-plugin
composer install
# Then symlink or copy the folder into wp-content/plugins/mddb-sync.
ln -s "$(pwd)" /path/to/wordpress/wp-content/plugins/mddb-sync
```

---

## Configuration

Settings live under **Settings → MDDB Sync** (or `wp-admin/options-general.php?page=mddb-sync`).

| Setting | Default | Notes |
|---|---|---|
| **MDDB URL** | _empty_ | Base URL, e.g. `https://mddb.example.com`. **Must be `https://`** so the API key is never sent in cleartext; `http://` is accepted only for local hosts (`localhost`, `127.0.0.1`, `::1`). Trailing slash is stripped. Plugin idles until this is set. |
| **API key** | _empty_ | Sent as `Authorization: Bearer <key>`. Leave empty for unauthenticated dev instances. |
| **Collection** | derived from site host (`example_com`) | One MDDB collection holds every synced post for this site. |
| **Sync on save / publish** | ✓ | Fires on `wp_after_insert_post`. Autosaves and revisions are ignored. |
| **Clean entries on trash / delete** | ✓ | Fires on `wp_trash_post` and `before_delete_post`. |
| **Include drafts** | ✗ | When off, only `publish` status syncs. |
| **Post types** | `post`, `page` | Any public post type can be toggled. |
| **Language detection** | Auto | Auto = Polylang → WPML → site locale. Or force one of the three. |
| **Key strategy** | `posttype-id` | Other options: `posttype-slug` or `permalink path`. |
| **Remote publishing (MCP)** | ✗ | Enables the `mddb-sync/v1` REST routes below. Off = the routes reject every request. |
| **Publish key** | _empty_ | Shared secret for inbound publishing. Leave empty and save with the toggle on to auto-generate a strong key. |

The **Test connection** button under the form calls `POST /v1/search` against your MDDB and reports the HTTP status.

### MDDB document shape

```json
{
  "collection": "example_com",
  "key": "post-42",
  "lang": "en_US",
  "meta": {
    "postType":   ["post"],
    "status":     ["publish"],
    "title":      ["Hello world"],
    "slug":       ["hello-world"],
    "permalink":  ["https://example.com/hello-world/"],
    "authorId":   ["3"],
    "author":     ["Jane Author"],
    "publishedAt": ["2026-05-19T10:14:00+00:00"],
    "modifiedAt":  ["2026-05-19T10:14:00+00:00"],
    "categories": ["News"],
    "tags":       ["intro", "demo"]
  },
  "contentMd": "# Hello world\n\nHello world.\n"
}
```

`contentMd` runs the post body through the standard `the_content` filter, then strips tags — Shortcodes, blocks, oEmbed etc. all get expanded first so the indexed text matches the rendered page.

---

## Remote publishing (MCP → WordPress)

Off by default. When **Remote publishing** is enabled and a **Publish key** is set, the plugin registers two REST routes under `mddb-sync/v1`. Every request must present the key as `Authorization: Bearer <key>` (or `X-MDDB-Publish-Key` for proxies that strip Authorization); keys are compared with `hash_equals`.

The natural caller is MDDB's built-in MCP server: pin the target once with `set_collection_config` (`wordpress: {url, api_key}`), then use the `wordpress_publish` / `wordpress_set_status` tools — see [docs/MCP.md](../../docs/MCP.md#wordpress-publishing-tools-v2110).

### `POST /wp-json/mddb-sync/v1/publish`

Create or update a post/page. Upserts by `id`, else by `type` + `slug`; creates when nothing matches (then `title` is required).

| Field | Type | Notes |
|---|---|---|
| `type` | string | Post type, default `post`. Must be in the plugin's post-type allow-list. |
| `id` | int | Update an existing post. |
| `slug` | string | URL slug; also the upsert match key when `id` is absent. |
| `title` | string | Required when creating. |
| `contentMarkdown` | string | Markdown body — converted by a built-in, HTML-escaping converter (headings, lists, links, images, code, blockquotes). |
| `contentHtml` | string | HTML body — sanitised with `wp_kses_post`. Wins over `contentMarkdown`. |
| `excerpt` | string | Optional excerpt. |
| `status` | string | `publish`, `draft` (default), `pending`, `private`, `future`. |
| `date` | string | ISO 8601; required for `future`. |
| `author` | int | WordPress user ID. |
| `tags` / `categories` | string[] | Term names; created when missing. |
| `taxonomies` | object | `{taxonomy: [term names]}` for custom taxonomies. |
| `meta` | object | Post meta ("metafields"): `{key: value}` — scalars or arrays. |
| `lang` | string | `pl_PL` / `pl` — assigned via Polylang or WPML. |
| `translationOf` | int | Post ID this post is a translation of; linked via `pll_save_post_translations` / `wpml_set_element_language_details`. |

Response: `{id, created, type, status, permalink, lang}`.

### `POST /wp-json/mddb-sync/v1/status`

Change publishing status: `{id | type+slug, status, date?}` with `status` ∈ `publish, draft, pending, private, future, trash`. Trash uses `wp_trash_post`; publishing a trashed post untrashes it first. Response: `{id, status, permalink}`.

Published/updated posts flow back to MDDB through the normal `wp_after_insert_post` sync, so the MDDB collection stays current automatically.

---

## Auto-updates

The plugin hooks into WordPress's own update mechanism by filtering `pre_set_site_transient_update_plugins` and `plugins_api`. It polls `https://api.github.com/repos/tradik/mddb/releases/latest` once every 12h (cached in a site transient) and looks for an asset named `mddb-sync-<version>.zip`. When the release tag is newer than the installed version, **Dashboard → Updates** shows the upgrade just like any wp.org plugin.

Releases are produced by the [`WordPress Plugin (mddb-sync)`](../../.github/workflows/wordpress-plugin.yml) workflow when a `wp-v*` tag is pushed (e.g. `wp-v0.2.0`).

---

## Development

```bash
make install     # composer install
make lint        # phpcs (WordPress security ruleset)
make stan        # phpstan level 5, WordPress-aware
make test        # phpunit
make coverage    # phpunit + xdebug coverage (text + clover.xml)
make audit       # composer audit (security advisories)
make ci          # audit → lint → stan → coverage
make build       # produce build/mddb-sync-<version>.zip (mirrors the GH Action)
```

### Layout

```
integrations/wordpress-plugin/
├── mddb-sync.php            # plugin header + autoloader bootstrap
├── includes/
│   ├── class-plugin.php     # orchestrator
│   ├── class-settings.php   # typed access to the option array
│   ├── class-admin.php      # Settings → MDDB Sync screen + AJAX probe
│   ├── class-client.php     # POST /v1/add, /v1/delete, /v1/search (probe)
│   ├── class-language.php   # Polylang / WPML / locale detection
│   ├── class-mapper.php     # WP_Post → MDDB document
│   ├── class-markdown.php   # Markdown → HTML (publishing payloads)
│   ├── class-publisher.php  # MCP publish/status → wp_insert_post & friends
│   ├── class-rest.php       # mddb-sync/v1 REST routes + bearer auth
│   ├── class-sync.php       # wp_after_insert_post / before_delete_post / wp_trash_post
│   ├── class-translations.php # Polylang / WPML language assignment + linking
│   └── class-updater.php    # GitHub Releases update channel
├── tests/                   # PHPUnit + Brain Monkey
├── composer.json
├── phpcs.xml                # WordPress security + DB + i18n + AlternativeFunctions
├── phpstan.neon.dist        # level 5 + szepeviktor/phpstan-wordpress
├── phpunit.xml.dist
└── Makefile
```

### Testing

134 tests, 93 %+ line coverage. The suite mocks every WordPress function via [Brain Monkey](https://github.com/Brain-WP/BrainMonkey) — no WordPress install needed.

CI matrix runs against PHP 8.2, 8.3, 8.4, 8.5. Coverage is enforced ≥90 % on the 8.3 leg.

---

## Hooks the plugin uses

| Type | Hook | What |
|---|---|---|
| Action | `wp_after_insert_post` | Build a document and call `/v1/add`. |
| Action | `before_delete_post` | Call `/v1/delete`. |
| Action | `wp_trash_post` | Same as delete — trash is treated as removal. |
| Filter | `pre_set_site_transient_update_plugins` | Inject "update available" payload from GitHub. |
| Filter | `plugins_api` | Provide plugin information for the "View details" popup. |
| Action | `rest_api_init` | Register the `mddb-sync/v1` publish/status routes (only active with Remote publishing on). |
| Action | `mddb_sync_error` (emitted) | Triggered with `($tag, WP_Error, $postId)` on every sync failure — wire your own Sentry/Action Scheduler glue here. |

---

## License

BSD-3-Clause — see [LICENSE](../LICENSE) for the integrations. The MDDB server itself is under the Business Source License 1.1 ([LICENSING.md](../../LICENSING.md)).
