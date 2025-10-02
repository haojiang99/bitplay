# YTS Sync Server

A caching proxy server for the YTS.mx API that syncs movie data periodically and provides identical API endpoints as a backup.

## Features

- **Automatic Caching**: Syncs the first 10 pages of movies from YTS.mx every 5 minutes
- **Identical API**: Provides the same API format as YTS.mx for drop-in replacement
- **Magnet Links**: Automatically generates magnet links with trackers for all torrents
- **Search Support**: Supports movie title search with dynamic caching
- **Health Monitoring**: Built-in health check endpoint

## Quick Start

### Run the Server

```bash
cd sync_server
go run main.go
```

The server will start on:
- **Local**: `http://localhost:8080`
- **External**: `http://66.42.87.30:8080` (accessible from internet)

### Build Executable

```bash
# macOS Intel
GOOS=darwin GOARCH=amd64 go build -o sync-server-intel main.go

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o sync-server-arm main.go

# macOS Universal Binary
lipo -create sync-server-intel sync-server-arm -output sync-server

# Windows
GOOS=windows GOARCH=amd64 go build -o sync-server.exe main.go

# Linux
GOOS=linux GOARCH=amd64 go build -o sync-server-linux main.go
```

## API Endpoints

### List Movies

```
GET /api/v2/list_movies.json
```

**Query Parameters:**
- `page` (optional, default: 1) - Page number
- `limit` (optional, default: 20) - Results per page
- `query_term` (optional) - Search query for movie titles

**Examples:**

```bash
# Get first page (served from cache) - Local
curl "http://localhost:8080/api/v2/list_movies.json?page=1&limit=20"

# Get first page - External
curl "http://66.42.87.30:8080/api/v2/list_movies.json?page=1&limit=20"

# Search for movies
curl "http://66.42.87.30:8080/api/v2/list_movies.json?query_term=inception"

# Get specific page
curl "http://66.42.87.30:8080/api/v2/list_movies.json?page=3&limit=20"
```

### Health Check

```
GET /health
```

Returns server status, cache size, and last sync time.

**Example:**

```bash
# Local
curl "http://localhost:8080/health"

# External
curl "http://66.42.87.30:8080/health"
```

**Response:**

```json
{
  "status": "ok",
  "lastSync": "2025-10-02T07:52:17-05:00",
  "cacheSize": 10,
  "syncInterval": "5m0s"
}
```

## How It Works

1. **Initial Sync**: On startup, the server fetches the first 10 pages (200 movies) from YTS.mx
2. **Periodic Sync**: Every 5 minutes, the cache is refreshed with the latest movies
3. **Cache Hits**: Cached requests are served instantly without hitting YTS.mx
4. **Cache Misses**: Uncached requests (like searches) are fetched from YTS.mx and then cached
5. **Magnet Links**: All torrents automatically get magnet URLs with popular trackers

## Configuration

Edit `main.go` to customize:

```go
const (
    YTS_API_URL    = "https://yts.mx/api/v2/list_movies.json"
    SYNC_INTERVAL  = 5 * time.Minute  // Change sync frequency
    MAX_PAGES      = 10                // Change number of pages to cache
    DEFAULT_PORT   = 8080              // Change server port
)
```

## Integration with Main Server

To use this as a backup in your main bitplay server, update the YTS API calls:

```go
// Instead of:
apiURL := "https://yts.mx/api/v2/list_movies.json?page=1"

// Use local sync server:
apiURL := "http://localhost:8080/api/v2/list_movies.json?page=1"

// Or use external sync server:
apiURL := "http://66.42.87.30:8080/api/v2/list_movies.json?page=1"
```

Or implement failover logic:

```go
// Try sync server first (external or local)
response, err := http.Get("http://66.42.87.30:8080/api/v2/list_movies.json?page=1")
if err != nil || response.StatusCode != 200 {
    // Fallback to YTS.mx
    response, err = http.Get("https://yts.mx/api/v2/list_movies.json?page=1")
}
```

## Benefits

1. **Faster Response**: Cached data is served instantly
2. **Reduced Load**: Fewer requests to YTS.mx
3. **Reliability**: Continues working even if YTS.mx is temporarily down (using cached data)
4. **Rate Limit Protection**: Avoids hitting YTS.mx rate limits

## Output Logging

The server logs important events:

```
[07:52:08] Starting cache sync...
[07:52:17] Cache sync completed. Cached 10 pages
[07:52:24] Serving from cache: page_1_limit_20
[07:52:38] Cache miss, fetching fresh: search_inception_page_1_limit_5
```

## Requirements

- Go 1.16 or higher
- Internet connection to fetch from YTS.mx

## License

Same as parent project
