# MDDB Panel

Web-based admin interface for MDDB (Markdown Database). A simple, modern single-page application for browsing and managing MDDB collections and documents.

## Features

- 📊 **Server Statistics** - View real-time database stats
- 📁 **Collection Browser** - Browse all collections
- 📄 **Document Viewer** - View document content and metadata
- 🔍 **Advanced Filtering** - Filter documents by metadata
- 📋 **Metadata Display** - Rich metadata visualization
- 🎨 **Modern UI** - Clean, responsive interface with TailwindCSS
- ⚡ **Fast** - Built with React 19 and Vite

## Screenshots

### Main Interface
- Sidebar with collections list and server stats
- Document list with metadata preview
- Document viewer with full content and metadata

### Features
- Filter documents by metadata fields
- Sort by date, key, or custom fields
- Copy document content
- View revision history

## Quick Start

### Prerequisites

- Node.js 24.3 or later
- npm 11.4.2 or later
- Running MDDB server (default: http://localhost:11023)

### Installation

```bash
# Navigate to panel directory
cd services/mddb-panel

# Install dependencies
npm install

# Start development server
npm run dev
```

The panel will be available at http://localhost:3000

### Production Build

```bash
# Build for production
npm run build

# Preview production build
npm run preview
```

## Configuration

### Environment Variables

Create a `.env` file in the panel directory:

```env
# MDDB Server URL (default: http://localhost:11023)
VITE_MDDB_SERVER=http://localhost:11023
```

### Proxy Configuration

The development server is configured to proxy API requests to MDDB server. See `vite.config.js` for details.

## Usage

### Browsing Collections

1. Server stats are displayed in the sidebar
2. Click on a collection to view its documents
3. Documents are listed with metadata preview

### Viewing Documents

1. Click on a document in the list
2. Full content and metadata are displayed
3. Use the copy button to copy markdown content

### Filtering Documents

1. Click the "Filters" button in the toolbar
2. Add metadata filters (key-value pairs)
3. Configure sort order and limit
4. Click "Apply Filters"

### Sorting

- Sort by: Added Date, Updated Date, or Key
- Order: Ascending or Descending
- Limit: Number of documents to display (1-1000)

## Architecture

### Tech Stack

- **React 19.1** - UI framework
- **Vite 6** - Build tool and dev server
- **TailwindCSS 4** - Styling
- **Zustand 5** - State management
- **Lucide React** - Icons
- **date-fns 4** - Date formatting

### Project Structure

```
services/mddb-panel/
├── src/
│   ├── components/          # React components
│   │   ├── Header.jsx       # Top navigation
│   │   ├── Sidebar.jsx      # Collections and stats
│   │   ├── DocumentList.jsx # Document browser
│   │   ├── DocumentViewer.jsx # Document details
│   │   └── FilterPanel.jsx  # Filter controls
│   ├── lib/
│   │   ├── mddb-client.js   # API client
│   │   └── store.js         # Global state
│   ├── App.jsx              # Main app component
│   ├── main.jsx             # Entry point
│   └── index.css            # Global styles
├── public/                  # Static assets
├── index.html               # HTML template
├── vite.config.js           # Vite configuration
├── tailwind.config.js       # Tailwind configuration
└── package.json             # Dependencies
```

### State Management

Uses Zustand for global state:
- Server statistics
- Current collection
- Documents list
- Current document
- Filters and sort options

### API Client

Simple REST client for MDDB HTTP API:
- `getStats()` - Server statistics
- `search()` - Search documents
- `getDocument()` - Get single document
- `addDocument()` - Add/update document
- `export()` - Export documents
- `backup()` - Create backup
- `truncate()` - Clean old revisions

## Development

### Running in Development

```bash
npm run dev
```

Features:
- Hot module replacement
- Fast refresh
- Proxy to MDDB server

### Building for Production

```bash
npm run build
```

Output in `dist/` directory.

### Linting

```bash
npm run lint
```

## Docker Support

### Dockerfile

```dockerfile
FROM node:24-alpine

WORKDIR /app

COPY package*.json ./
RUN npm ci --production

COPY . .
RUN npm run build

EXPOSE 3000

CMD ["npm", "run", "preview"]
```

### Docker Compose

```yaml
services:
  mddb-panel:
    build: ./services/mddb-panel
    ports:
      - "3000:3000"
    environment:
      - VITE_MDDB_SERVER=http://mddbd:11023
    depends_on:
      - mddbd
```

## API Integration

The panel connects to MDDB HTTP API at `/v1` endpoints:

- `GET /v1/stats` - Server statistics
- `POST /v1/search` - Search documents
- `POST /v1/get` - Get document
- `POST /v1/add` - Add/update document
- `POST /v1/export` - Export documents
- `GET /v1/backup` - Create backup
- `POST /v1/truncate` - Truncate revisions

## Browser Support

- Chrome/Edge (latest)
- Firefox (latest)
- Safari (latest)

## Contributing

1. Follow React best practices
2. Use functional components with hooks
3. Keep components small and focused
4. Use TailwindCSS for styling
5. Test on multiple browsers

## License

MIT License - see LICENSE file for details

## See Also

- [MDDB Documentation](../../docs/)
- [API Documentation](../../docs/API.md)
- [MDDB Server](../mddbd/)
