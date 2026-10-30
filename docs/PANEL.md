---
title: "MDDB Panel - Web Admin Interface"
slug: "docs/panel"
description: "MDDB Panel, the React admin interface: browse collections and documents, run every search type, inspect vectors and geo data, and manage users."
status: publish
---

# MDDB Panel - Web Admin Interface

## Overview

MDDB Panel is a modern, web-based admin interface for MDDB (Markdown Database). It provides a visual way to browse collections, view documents, filter by metadata, and manage your markdown database without using the command line.

## Features

### 📊 Server Statistics Dashboard
- Real-time database statistics
- Total documents and revisions count
- Database size monitoring
- Collections overview

### 📁 Collection Browser
- List all collections with document counts
- Quick collection switching
- Visual collection organization

### 📄 Document Management
- Browse documents with metadata preview
- View full document content
- Display rich metadata
- **Edit documents** - Modify content and metadata
- **Markdown editor with live preview** - Split view with real-time rendering
- **Markdown toolbar** - Quick formatting (bold, italic, headings, lists, tables, etc.)
- **Syntax highlighting** - Code blocks with 100+ language support
- **Templates** - Pre-built templates for common document types
- **Create new documents** - Add documents directly from UI
- Copy markdown content to clipboard
- View document revision information

### 🌐 API Endpoints Browser
- Tabbed view of all HTTP, gRPC, and MCP endpoints
- HTTP tab with method badges, paths, descriptions, and auth status icons
- gRPC tab with method names and descriptions
- MCP tab with all 44 built-in tool names and descriptions
- Link to versioned OpenAPI spec on GitHub (auto-detects server version)

### 🔍 Advanced Filtering
- Filter documents by metadata fields
- Multiple filter criteria support
- Sort by date, key, or custom fields
- Ascending/descending order
- Configurable result limits

### 🗺️ Vector Space Explorer

- 2D PCA projection of a collection's embeddings (server-side, sampled)
- Each dot is a document chunk; colors group chunks of the same document
- Hover/click to inspect points; keyboard focusable (WCAG 2.2)
- Optional query overlay: project a natural-language query into the map to
  see why results match ("Vector Space" in the sidebar)

### Server features of 2.18.9 and 2.18.10

- **Corpus packs** (System page): installed packs with version, licence,
  document count and a read-only badge, a warning when a pack's vectors are
  disabled (another embedding model), install from a `.mddbpack` file,
  remove after a confirm, and build-and-download of a collection's pack.
  A read-only collection shows a notice in its document list and settings,
  and its Upload and delete controls are off. Needs admin on `"*"`.
- **Diagnose bundle** (System page): downloads `GET /v1/admin/diagnose` with
  the opt-in profiles, integrity check and real collection names.
- **Automations**: a cron rule's action is a webhook, a scheduled backup
  (local or S3, retention) or a synced source (sitemap, URL list, S3 prefix);
  the log shows backup targets and sync counts.
- **Search playground**: vector index `Auto (server chooses)` on vector,
  hybrid and cross-collection search; *Explain plan* shows how the search ran
  (index, filter strategy, candidates, reasons, feedback, query parts);
  *Split multi-part questions* sends `queryDecomposition: "auto"`; vector
  results can be marked relevant / not relevant and the next search sends
  them as relevance feedback.
- **Result cap**: when a search answer says `limitCapped` (or `/v1/search`
  sends `X-Limit-Capped`), a notice names the endpoint and the limit.
- **Collection settings**: vector index, summary mode and weight, result cap
  (`maxTopK`), meta keys kept out of full-text search
  (`fts.excludeMetaKeys`) and the quota. Saving starts from the stored
  config, so settings the dialog does not show are kept.
- **Vector Search page**: the background training queue (`indexTraining`) and
  a per-collection training badge; a search naming an index still training
  gets the server's 503 message and is not retried.
- **Permissions**: admin badges read *Admin (global)* on `"*"` and *Admin
  (this collection)* elsewhere, and the permission forms explain that only
  admin on `"*"` is server-wide. A `403` names that rule; a `READ_ONLY`
  refusal shows the server's message.
- **Search advisor**: recommended summary mode, the share of documents with a
  summary, and *Matches by meaning: No* for the lexical provider.

Server settings with no API to read them (PROXY protocol, trusted proxies,
OpenTelemetry export, the Elasticsearch-compatible `/es` subset,
`MDDB_SEARCH_MAX_LIMIT`) are not shown.

### 🎨 Modern UI
- Clean, responsive design
- TailwindCSS styling
- Lucide React icons
- Smooth animations and transitions
- Mobile-friendly interface

## Quick Start

### Prerequisites

- Node.js 24.3 or later
- npm 11.4.2 or later
- Running MDDB server

### Installation

```bash
# Navigate to panel directory
cd services/mddb-panel

# Install dependencies
npm install
```

Or use Makefile:

```bash
make panel-install
```

### Development

```bash
# Start development server
npm run dev

# Or use Makefile
make panel-dev
```

Access the panel at http://localhost:3000

### Production

```bash
# Build for production
npm run build

# Start production server (with reverse proxy to mddbd)
npm start

# Or use Makefile
make panel-build
```

## Panel Modes

MDDB Panel supports two deployment modes controlled by `MDDB_PANEL_MODE` on the mddbd server:

### Internal Mode (Default)

The panel and mddbd run together (same host or Docker network). CORS is enabled on mddbd so the browser can make cross-origin requests directly to the API.

```
Browser -> Panel (:3000)     -> static files
Browser -> mddbd (:11023)    -> API (with CORS headers)
```

### External Mode

The panel runs as a separate service with its own reverse proxy. All API requests from the browser go through the panel's server, which proxies them to mddbd. No CORS needed.

```
Browser -> Panel (:3000) /v1/* -> proxy -> mddbd (:11023)
Browser -> Panel (:3000) /*    -> static files
```

Set on mddbd:
```bash
MDDB_PANEL_MODE=external
```

Set on panel container:
```bash
MDDB_SERVER=http://mddbd:11023
```

External mode is recommended when:
- Panel is deployed on a different host than mddbd
- You want to avoid CORS headers in API responses
- You need a single entry point for both UI and API

## Docker Deployment

### Using Docker Compose

The easiest way to run MDDB Panel with the server:

```bash
# Start both server and panel
docker-compose up -d

# Access panel at http://localhost:3000
# Server API at http://localhost:11023
```

### Standalone Docker

```bash
# Build image
cd services/mddb-panel
docker build -t mddb-panel .

# Run container
docker run -d \
  -p 3000:3000 \
  -e MDDB_SERVER=http://mddb-server:11023 \
  --name mddb-panel \
  mddb-panel
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_SERVER` | `http://localhost:11023` | mddbd server URL (used by production proxy and dev server) |
| `PORT` | `3000` | Panel server port |

### Proxy Configuration

Both development and production servers proxy `/v1/*` requests to mddbd:

- **Development**: Vite dev server proxy (configured in `vite.config.js`)
- **Production**: Express reverse proxy (`server.js`)

## Usage Guide

### Browsing Collections

1. **View Server Stats**: The sidebar shows real-time statistics including total documents, revisions, and database size.

2. **Select Collection**: Click on any collection in the sidebar to view its documents.

3. **View Document Count**: Each collection shows the number of documents it contains.

### Viewing Documents

1. **Document List**: After selecting a collection, documents are displayed in a list with:
   - Document key
   - Language code
   - Last updated date
   - Metadata preview (first 2 tags)

2. **Open Document**: Click on any document to view its full content and metadata.

3. **Document Viewer**: Shows:
   - Full markdown content
   - All metadata fields
   - Creation and update timestamps
   - Revision number
   - Collection name

4. **Copy Content**: Use the "Copy" button to copy markdown content to clipboard.

### Filtering Documents

1. **Open Filters**: Click the "Filters" button in the toolbar.

2. **Add Metadata Filters**:
   - Enter filter key (e.g., "author")
   - Enter filter value (e.g., "John Doe")
   - Click "Add Filter"

3. **Configure Sort**:
   - Choose sort field (Added Date, Updated Date, Key)
   - Select order (Ascending/Descending)

4. **Set Limit**: Specify maximum number of documents to display (1-1000).

5. **Apply**: Click "Apply Filters" to update the document list.

6. **Clear**: Use "Clear All" to remove all filters.

### Editing Documents

1. **Open Document**: Click on a document to view it.

2. **Click Edit**: Click the "Edit" button in the document viewer.

3. **Use Markdown Editor**:
   - **Edit Mode**: Write markdown in the editor
   - **Preview Mode**: See rendered markdown with syntax highlighting
   - **Split Mode**: Edit and preview side-by-side (default)
   - **Fullscreen**: Toggle fullscreen for better focus
   - **Toolbar**: Quick formatting buttons
     - Headings (H1, H2, H3)
     - Bold, Italic, Strikethrough
     - Inline code
     - Links, Lists (bullet, numbered, tasks)
     - Blockquotes, Tables
   - **Templates**: Choose from pre-built templates
     - Blog Post
     - Documentation
     - README
     - API Documentation
     - Changelog
   - Real-time preview with syntax highlighting
   - Support for GitHub Flavored Markdown (tables, task lists, etc.)
   - Code blocks with 100+ language support

4. **Modify Content**:
   - Edit markdown content with live preview
   - Add, remove, or update metadata fields
   - See character, line, and word count
   - All changes are tracked

5. **Save Changes**: Click "Save Changes" to update the document.

6. **Success**: Document is updated and a new revision is created.

### Creating New Documents

1. **Select Collection**: Choose the collection where you want to add a document.

2. **Click New Document**: Click the "New Document" button in the toolbar.

3. **Fill Details**:
   - Enter unique document key
   - Select language
   - Add metadata (optional)
   - Write markdown content

4. **Create**: Click "Create Document" to save.

### Keyboard Shortcuts

- `Esc` - Close document viewer, editor, or filter panel
- `Ctrl/Cmd + C` - Copy document content (when viewer is open)
- `Enter` - Add metadata field (when in metadata input)

## Architecture

### Tech Stack

- **React 19.1** - Modern UI framework with latest features
- **Vite 6** - Fast build tool and dev server
- **TailwindCSS 4** - Utility-first CSS framework
- **Zustand 5** - Lightweight state management
- **Lucide React 0.544** - Beautiful icon library
- **date-fns 4** - Modern date utility library
- **react-markdown 10** - Markdown rendering component
- **remark-gfm 4** - GitHub Flavored Markdown support
- **rehype-raw/sanitize** - Safe HTML rendering
- **react-syntax-highlighter** - Code syntax highlighting
- **prismjs** - Syntax highlighting engine (100+ languages)

### Project Structure

```
services/mddb-panel/
├── src/
│   ├── components/
│   │   ├── Header.jsx           # Top navigation bar
│   │   ├── Sidebar.jsx          # Collections and stats
│   │   ├── DocumentList.jsx     # Document browser
│   │   ├── DocumentViewer.jsx   # Document details view
│   │   ├── DocumentEditor.jsx   # Document editing modal
│   │   ├── NewDocumentModal.jsx # New document creation
│   │   ├── MarkdownEditor.jsx   # Markdown editor with preview
│   │   ├── MarkdownToolbar.jsx  # Formatting toolbar
│   │   ├── EndpointsPanel.jsx   # API endpoints browser
│   │   └── FilterPanel.jsx      # Filter controls
│   ├── lib/
│   │   ├── mddb-client.js       # API client
│   │   ├── markdown-templates.js # Document templates
│   │   └── store.js             # Zustand store
│   ├── App.jsx                  # Main application
│   ├── main.jsx                 # Entry point
│   └── index.css                # Global styles
├── public/                      # Static assets
├── index.html                   # HTML template
├── vite.config.js               # Vite configuration
├── tailwind.config.js           # Tailwind configuration
├── postcss.config.js            # PostCSS configuration
├── package.json                 # Dependencies
├── Dockerfile                   # Docker build
└── README.md                    # Panel documentation
```

### State Management

The panel uses Zustand for global state management:

```javascript
// Global state includes:
- stats              // Server statistics
- currentCollection  // Selected collection
- documents          // Document list
- currentDocument    // Selected document
- filters            // Active filters
- sortBy             // Sort field
- sortAsc            // Sort order
- limit              // Result limit
```

### API Client

The panel communicates with MDDB server via HTTP API:

```javascript
// Available methods:
- getStats()         // Get server statistics
- search()           // Search documents
- getDocument()      // Get single document
- addDocument()      // Add/update document
- export()           // Export documents
- backup()           // Create backup
- truncate()         // Clean old revisions
- getEndpoints()     // List all API endpoints
```

## API Integration

The panel uses the following MDDB API endpoints:

### GET /v1/stats
Get server statistics including document count, database size, and collections.

### POST /v1/search
Search documents with filters:
```json
{
  "collection": "blog",
  "filterMeta": {
    "author": ["John Doe"]
  },
  "sort": "updatedAt",
  "asc": false,
  "limit": 100
}
```

### GET /v1/endpoints
Get all available HTTP, gRPC, and MCP endpoints with descriptions and auth status.

### POST /v1/get
Get a specific document:
```json
{
  "collection": "blog",
  "key": "hello-world",
  "lang": "en_US"
}
```

## Customization

### Styling

The panel uses TailwindCSS for styling. Customize colors in `tailwind.config.js`:

```javascript
theme: {
  extend: {
    colors: {
      primary: {
        50: '#f0f9ff',
        // ... customize colors
      },
    },
  },
}
```

### Components

All components are in `src/components/` and can be customized:

- `Header.jsx` - Top navigation
- `Sidebar.jsx` - Left sidebar with collections
- `DocumentList.jsx` - Document browser
- `DocumentViewer.jsx` - Document details
- `FilterPanel.jsx` - Filter controls

### API Client

Extend the API client in `src/lib/mddb-client.js` to add new endpoints or functionality.

## Performance

### Optimization Features

- **Code Splitting** - Vite automatically splits code for faster loading
- **Tree Shaking** - Unused code is removed in production builds
- **Minification** - JavaScript and CSS are minified
- **Compression** - Gzip compression for smaller bundle sizes
- **Lazy Loading** - Components load on demand
- **Memoization** - React components use proper memoization

### Build Optimization

```bash
# Production build with optimizations
npm run build

# Analyze bundle size
npm run build -- --mode analyze
```

## Troubleshooting

### Panel won't start

**Issue**: Development server fails to start

**Solution**:
```bash
# Clear node_modules and reinstall
rm -rf node_modules package-lock.json
npm install
```

### Can't connect to MDDB server

**Issue**: API requests fail with connection errors

**Solution**:
1. Ensure MDDB server is running on port 11023
2. Check `VITE_MDDB_SERVER` environment variable
3. Verify proxy configuration in `vite.config.js`

### Build fails

**Issue**: Production build fails

**Solution**:
```bash
# Check Node.js version (must be 24.3+)
node --version

# Update dependencies
npm update

# Clear cache and rebuild
rm -rf dist node_modules
npm install
npm run build
```

### Filters not working

**Issue**: Filters don't affect document list

**Solution**:
1. Click "Apply Filters" after adding filters
2. Check browser console for errors
3. Verify MDDB server supports the filter fields

## Browser Support

- Chrome/Edge 90+
- Firefox 88+
- Safari 14+
- Opera 76+

## Security Considerations

### Production Deployment

1. **Use HTTPS**: Always deploy with HTTPS in production
2. **Environment Variables**: Never commit `.env` files
3. **API Authentication**: Consider adding authentication to MDDB server
4. **CORS**: Configure CORS properly on MDDB server
5. **Content Security Policy**: Add CSP headers

### Best Practices

- Keep dependencies updated
- Use environment variables for configuration
- Enable HTTPS for production
- Implement proper error handling
- Monitor server logs

## Contributing

### Development Workflow

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

### Code Style

- Use functional components with hooks
- Follow React best practices
- Use TailwindCSS for styling
- Keep components small and focused
- Add comments for complex logic

### Testing

```bash
# Run linter
npm run lint

# Fix linting issues
npm run lint -- --fix

# Every suite once, with the per-file coverage check (97%)
npm test
```

Files listed in `coverageFiles` in `vitest.config.js` must keep at least 97% of
their statements, branches, functions and lines covered, per file. See the
panel's README for how the suites are written.

## Roadmap

### Completed Features

- [x] Document editing interface
- [x] Document creation from UI
- [x] Metadata editor
- [x] Markdown editor with live preview
- [x] Split-view editing (edit/preview/both)
- [x] Fullscreen editing mode
- [x] GitHub Flavored Markdown support
- [x] Markdown toolbar with formatting buttons
- [x] Syntax highlighting for code blocks (100+ languages)
- [x] Document templates (blog, docs, README, API, changelog)
- [x] API Endpoints browser with HTTP/gRPC/MCP tabs
- [x] OpenAPI spec link with auto-detected version

### Planned Features

- [ ] Document deletion
- [ ] Bulk operations (delete, export multiple)
- [ ] Advanced search with full-text
- [ ] Document comparison (diff view)
- [ ] Revision history viewer with diff
- [ ] User authentication
- [ ] Role-based access control
- [ ] Dark mode
- [ ] More keyboard shortcuts
- [ ] Export to various formats (PDF, HTML)
- [ ] Import from files (drag & drop)
- [ ] Collection management (create, delete)
- [ ] Server configuration UI
- [ ] More syntax themes (light/dark)
- [ ] Custom markdown templates
- [ ] Markdown shortcuts (Ctrl+B for bold, etc.)
- [ ] Auto-save drafts
- [ ] Spell checker

## See Also

- [MDDB Documentation](/docs/readme/)
- [API Documentation](API.md)
- [Architecture](ARCHITECTURE.md)
- [Installation & CLI](INSTALLATION.md)

## License

BSD 3-Clause License - see [LICENSE](https://github.com/tradik/mddb/blob/main/LICENSE) for details.
