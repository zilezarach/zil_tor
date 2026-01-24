# Zil Torrent API

A high-performance, unified API for searching and downloading content from multiple indexers including torrents, books, and game repacks. Features built-in Cloudflare/DDOS-Guard bypass, intelligent caching, and concurrent search capabilities.

## ✨ Features

- **🔍 Multi-Indexer Search**: Search across YTS, 1337x, FitGirl, DODI, LibGen, and Anna's Archive
- **📚 Book Downloads**: Direct downloads from LibGen and Anna's Archive with automatic mirror selection
- **🎮 Game Repacks**: Browse latest and popular releases from FitGirl and DODI
- **🛡️ Built-in Protection Bypass**: Automatic Cloudflare and DDOS-Guard bypass using FlareSolverr
- **⚡ Smart Caching**: 5-minute cache to reduce redundant requests
- **🔄 Concurrent Processing**: Fast parallel searches across multiple indexers
- **📊 Health Monitoring**: Real-time health checks and statistics
- **🐳 Docker Ready**: Fully containerized with Docker Compose

## 📋 Table of Contents

- [Quick Start](#-quick-start)
- [Installation](#-installation)
- [Configuration](#-configuration)
- [API Documentation](#-api-documentation)
- [Usage Examples](#-usage-examples)
- [Troubleshooting](#-troubleshooting)
- [Advanced Configuration](#-advanced-configuration)

---

## 🚀 Quick Start

### Prerequisites

- Docker & Docker Compose installed
- 2GB RAM minimum (4GB recommended)
- Internet connection

### One-Command Setup

```bash
# Clone the repository
git clone <your-repo-url>
cd zil_tor-api

# Start everything
docker-compose up -d

# Check status
docker-compose ps

# View logs
docker-compose logs -f
```

**That's it!** 🎉 The API is now running at `http://localhost:9117`

Test it:

```bash
curl http://localhost:9117/api/v1/health
```

---

## 📥 Installation

### Option 1: Docker Compose (Recommended)

```bash
# 1. Clone the repository
git clone <your-repo-url>
cd zil_tor-api

# 2. (Optional) Edit configuration in docker-compose.yml
nano docker-compose.yml

# 3. Start services
docker-compose up -d

# 4. Verify everything is running
docker-compose ps
curl http://localhost:9117/api/v1/health
```

### Option 2: Manual Build

```bash
# Build the image
docker build -t zil_tor-api .

# Run with FlareSolverr
docker run -d --name flaresolverr -p 8191:8191 ghcr.io/flaresolverr/flaresolverr:latest

docker run -d \
  --name zil_tor-api \
  -p 9117:9117 \
  -e FLARESOLVERR_URL=http://flaresolverr:8191 \
  zil_tor-api
```

### Option 3: Local Development

```bash
# Install Go 1.21+
# Clone repo
git clone <your-repo-url>
cd zil_tor-api

# Install dependencies
go mod download

# Run FlareSolverr separately
docker run -d -p 8191:8191 ghcr.io/flaresolverr/flaresolverr:latest

# Start the API
go run cmd/server/main.go
```

---

## ⚙️ Configuration

### Environment Variables

Edit these in `docker-compose.yml` or set them in your environment:

| Variable           | Default                    | Description                           |
| ------------------ | -------------------------- | ------------------------------------- |
| `PORT`             | `9117`                     | API server port                       |
| `CACHE_ENABLED`    | `true`                     | Enable/disable result caching         |
| `SOLVER_ENABLED`   | `true`                     | Enable Cloudflare/DDOS-Guard bypass   |
| `FLARESOLVERR_URL` | `http://flaresolverr:8191` | FlareSolverr endpoint                 |
| `LOG_LEVEL`        | `info`                     | Logging level (debug/info/warn/error) |

### Quick Configuration Examples

**Disable caching:**

```yaml
environment:
  - CACHE_ENABLED=false
```

**Change port:**

```yaml
ports:
  - "8080:8080" # Change host port
environment:
  - PORT=8080 # Change container port
```

**Enable debug logging:**

```yaml
environment:
  - LOG_LEVEL=debug
```

### Port Configuration

- **9117**: Main API (change if needed)
- **8191**: FlareSolverr (usually keep default)

To change the main API port:

```yaml
services:
  zil_tor-api:
    ports:
      - "8080:9117" # Access API on port 8080
```

---

## 📚 API Documentation

Base URL: `http://localhost:9117/api/v1`

### 🔍 General Search

Search across all torrent indexers (YTS, 1337x):

```bash
GET /search?query=<search_term>&limit=<number>&category=<category>
```

**Parameters:**

- `query` (required): Search term
- `limit` (optional): Results per indexer (default: 50, max: 100)
- `category` (optional): Filter by category (movies, tv, games, etc.)

**Example:**

```bash
curl "http://localhost:9117/api/v1/search?query=inception&limit=10"
```

**Response:**

```json
{
  "query": "inception",
  "results": [
    {
      "title": "Inception (2010)",
      "magnet": "magnet:?xt=urn:btih:...",
      "size": "2.5 GB",
      "seeders": 1234,
      "leechers": 56,
      "source": "YTS",
      "category": "movies"
    }
  ],
  "total_found": 42,
  "search_time_ms": 2341.5,
  "cache_hit": false
}
```

---

### 📖 Book Search & Download

#### 🔥 Unified Search (Recommended)

Search **both LibGen and Anna's Archive** simultaneously for maximum results:

```bash
GET /books/search?query=<search_term>&limit=<number>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/search?query=golang&limit=25"
```

**Response:**

```json
{
  "query": "golang",
  "results": [
    {
      "title": "The Go Programming Language",
      "author": "Alan Donovan",
      "md5": "abc123...",
      "extension": "pdf",
      "size": "5.2 MB",
      "year": "2015",
      "source": "LibGen"
    },
    {
      "title": "Learning Go",
      "author": "Jon Bodner",
      "md5": "def456...",
      "extension": "epub",
      "size": "3.1 MB",
      "year": "2021",
      "source": "AnnasArchive"
    }
  ],
  "count": 42,
  "total_search_time_ms": 3421.5,
  "sources": {
    "libgen": 1234.5,
    "annas_archive": 2187.0
  },
  "cached": false
}
```

**Features:**

- ✅ Searches both sources concurrently
- ✅ Removes duplicates based on MD5
- ✅ Sorts by relevance to your query
- ✅ Shows which source each book came from
- ✅ Faster than searching individually

#### Search Specific Source

Search only one indexer:

```bash
GET /books/search/source?query=<search_term>&source=<libgen|annas|both>&limit=<number>
```

**Example:**

```bash
# Only LibGen
curl "http://localhost:9117/api/v1/books/search/source?query=python&source=libgen&limit=10"

# Only Anna's Archive
curl "http://localhost:9117/api/v1/books/search/source?query=python&source=annas&limit=10"

# Both (same as /books/search)
curl "http://localhost:9117/api/v1/books/search/source?query=python&source=both&limit=10"
```

#### Search Books (LibGen Only)

```bash
GET /books/libgen/search?query=<search_term>&limit=<number>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/libgen/search?query=golang&limit=10"
```

**Response:**

```json
{
  "query": "golang",
  "results": [
    {
      "title": "The Go Programming Language",
      "author": "Alan Donovan",
      "md5": "abc123...",
      "extension": "pdf",
      "size": "5.2 MB",
      "year": "2015",
      "mirrors": ["http://library.lol/main/...", "http://libgen.rs/book/..."]
    }
  ],
  "count": 10,
  "source": "LibGen"
}
```

#### 🚀 Smart Download (Recommended)

Intelligent download that automatically tries both sources:

```bash
GET /books/download?md5=<md5_hash>&source=<auto|libgen|annas>
```

**Parameters:**

- `md5` (required): The MD5 hash of the book
- `source` (optional):
  - `auto` (default): Tries LibGen first (faster), falls back to Anna's if needed
  - `libgen`: Only use LibGen
  - `annas`: Only use Anna's Archive

**Example:**

```bash
# Auto mode (tries LibGen first, then Anna's)
curl "http://localhost:9117/api/v1/books/download?md5=abc123..."

# Force specific source
curl "http://localhost:9117/api/v1/books/download?md5=abc123...&source=libgen"
```

**Response:**

```json
{
  "md5": "abc123...",
  "download_url": "https://download.library.lol/main/...",
  "source": "LibGen",
  "cached": false
}
```

**Features:**

- ✅ Tries LibGen first (2-5 seconds)
- ✅ Automatically falls back to Anna's Archive if LibGen fails
- ✅ Caches URLs for 30 minutes
- ✅ Returns which source was used

#### Download Book (LibGen)

```bash
GET /books/libgen/download?md5=<md5_hash>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/libgen/download?md5=abc123..."
```

**Response:**

```json
{
  "md5": "abc123...",
  "download_url": "https://download.library.lol/main/...",
  "cached": true
}
```

#### Search Books (Anna's Archive)

```bash
GET /books/annas/search?query=<search_term>&limit=<number>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/annas/search?query=python&limit=10"
```

#### Download Book (Anna's Archive)

```bash
GET /books/annas/download?md5=<md5_hash>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/annas/download?md5=94c18bda57d811a59ef70c695ef28933"
```

**Response:**

```json
{
  "md5": "94c18bda57d811a59ef70c695ef28933",
  "format": "epub",
  "download_url": "https://b4mcx2ml.net/d3/y/...",
  "title": "Golang: The Ultimate Guide",
  "cached": false
}
```

**Note:** This endpoint automatically bypasses DDOS-Guard protection and returns the direct download URL.

#### Proxy Download (Anna's Archive)

Download the file directly through the API:

```bash
GET /books/annas/download/proxy?md5=<md5_hash>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/books/annas/download/proxy?md5=94c18bda57d811a59ef70c695ef28933" -o book.epub
```

---

### 🎮 Game Repacks

#### Search All Repacks

```bash
GET /games/repacks/search?query=<game_name>&limit=<number>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/games/repacks/search?query=cyberpunk&limit=10"
```

#### Latest Releases

```bash
# All repacks
GET /games/repacks/latest?limit=<number>

# FitGirl only
GET /games/fitgirl/latest?limit=<number>

# DODI only
GET /games/dodi/latest?limit=<number>
```

**Example:**

```bash
curl "http://localhost:9117/api/v1/games/repacks/latest?limit=20"
```

#### Popular Releases

```bash
# FitGirl popular
GET /games/fitgirl/popular?limit=<number>

# DODI popular
GET /games/dodi/popular?limit=<number>
```

---

### 🏥 System Endpoints

#### Health Check

```bash
GET /health
```

**Example:**

```bash
curl http://localhost:9117/api/v1/health
```

**Response:**

```json
{
  "status": "healthy",
  "healthy_count": 6,
  "total_indexers": 6,
  "cache_enabled": true,
  "solver_enabled": true,
  "uptime": "2h15m30s",
  "indexers": {
    "YTS": true,
    "1337x": true,
    "FitGirl": true,
    "DODI": true,
    "libgen": true,
    "annas": true
  }
}
```

#### Statistics

```bash
GET /stats
```

**Response:**

```json
{
  "uptime": "2h15m30s",
  "version": "1.0.0",
  "cache_size": 42,
  "cache_enabled": true,
  "indexers": 6,
  "memory_mb": 156,
  "goroutines": 23
}
```

#### List Indexers

```bash
GET /indexers
```

**Response:**

```json
{
  "indexers": [
    { "name": "YTS", "enabled": true, "healthy": true },
    { "name": "1337x", "enabled": true, "healthy": true },
    { "name": "FitGirl", "enabled": true, "healthy": true }
  ],
  "total": 6
}
```

---

## 💡 Usage Examples

### Python

```python
import requests

# 🔥 Unified book search (searches both LibGen and Anna's Archive)
def search_books(query, limit=25):
    response = requests.get(
        "http://localhost:9117/api/v1/books/search",
        params={"query": query, "limit": limit}
    )
    return response.json()

# Smart download (tries both sources automatically)
def download_book(md5):
    # Get download URL (tries LibGen first, then Anna's)
    response = requests.get(
        "http://localhost:9117/api/v1/books/download",
        params={"md5": md5}
    )
    download_info = response.json()

    print(f"Source: {download_info['source']}")
    print(f"Download URL: {download_info['download_url']}")

    # Download the actual file
    book_response = requests.get(download_info['download_url'], stream=True)
    with open('book.pdf', 'wb') as f:
        for chunk in book_response.iter_content(chunk_size=8192):
            f.write(chunk)

    return download_info

# Example usage
results = search_books("python programming", limit=10)
print(f"Found {results['count']} books from {len(results['sources'])} sources")
print(f"Search took {results['total_search_time_ms']:.2f}ms")

for book in results['results'][:5]:
    print(f"- {book['title']} by {book.get('author', 'Unknown')}")
    print(f"  Source: {book['source']}, Format: {book['extension']}")

# Download first book
if results['results']:
    first_book = results['results'][0]
    print(f"\nDownloading: {first_book['title']}")
    download_book(first_book['md5'])
    print("Download complete!")
```

**Advanced: Search specific source**

```python
# Search only LibGen
libgen_results = requests.get(
    "http://localhost:9117/api/v1/books/search/source",
    params={"query": "golang", "source": "libgen", "limit": 10}
).json()

# Search only Anna's Archive
annas_results = requests.get(
    "http://localhost:9117/api/v1/books/search/source",
    params={"query": "golang", "source": "annas", "limit": 10}
).json()
```

**Old method (still works):**

```python
import requests

# Search for torrents
response = requests.get(
    "http://localhost:9117/api/v1/search",
    params={"query": "ubuntu", "limit": 10}
)
results = response.json()

# Download book from Anna's Archive (direct)
response = requests.get(
    "http://localhost:9117/api/v1/books/annas/download",
    params={"md5": "94c18bda57d811a59ef70c695ef28933"}
)
download_info = response.json()
print(f"Download URL: {download_info['download_url']}")

# Download the actual file
book_response = requests.get(download_info['download_url'], stream=True)
with open('book.epub', 'wb') as f:
    for chunk in book_response.iter_content(chunk_size=8192):
        f.write(chunk)
```

### JavaScript/Node.js

```javascript
const axios = require("axios");

// 🔥 Unified search - searches both LibGen and Anna's Archive
const searchBooks = async (query, limit = 25) => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/search",
    {
      params: { query, limit },
    },
  );
  return response.data;
};

// Smart download - tries both sources automatically
const downloadBook = async (md5, source = "auto") => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/download",
    {
      params: { md5, source },
    },
  );

  console.log(`Source used: ${response.data.source}`);
  console.log(`Cached: ${response.data.cached}`);

  return response.data.download_url;
};

// Example usage
(async () => {
  // Search both sources
  const results = await searchBooks("javascript programming", 10);
  console.log(`Found ${results.count} books`);
  console.log(`Search time: ${results.total_search_time_ms.toFixed(2)}ms`);
  console.log(`LibGen: ${results.sources.libgen.toFixed(2)}ms`);
  console.log(`Anna's Archive: ${results.sources.annas_archive.toFixed(2)}ms`);

  // Display results
  results.results.slice(0, 5).forEach((book, i) => {
    console.log(`${i + 1}. ${book.title} by ${book.author || "Unknown"}`);
    console.log(
      `   Source: ${book.source}, Format: ${book.extension}, Size: ${book.size}`,
    );
  });

  // Download first book
  if (results.results.length > 0) {
    const firstBook = results.results[0];
    console.log(`\nDownloading: ${firstBook.title}`);
    const downloadUrl = await downloadBook(firstBook.md5);
    console.log(`Download URL: ${downloadUrl}`);
  }
})();
```

**Search specific source:**

```javascript
// Only LibGen (faster)
const libgenOnly = async (query) => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/search/source",
    {
      params: { query, source: "libgen", limit: 10 },
    },
  );
  return response.data;
};

// Only Anna's Archive (more comprehensive)
const annasOnly = async (query) => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/search/source",
    {
      params: { query, source: "annas", limit: 10 },
    },
  );
  return response.data;
};
```

**Old method (still works):**

```javascript
const axios = require("axios");

// Search books
const searchBooks = async (query) => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/libgen/search",
    {
      params: { query, limit: 10 },
    },
  );
  return response.data;
};

// Download from Anna's Archive
const downloadBook = async (md5) => {
  const response = await axios.get(
    "http://localhost:9117/api/v1/books/annas/download",
    {
      params: { md5 },
    },
  );
  return response.data.download_url;
};

// Usage
searchBooks("python programming").then(console.log);
downloadBook("94c18bda57d811a59ef70c695ef28933").then((url) => {
  console.log("Download URL:", url);
});
```

### cURL Scripts

**🔥 Unified search and download workflow:**

```bash
#!/bin/bash

# 1. Search both LibGen AND Anna's Archive
QUERY="golang programming"
echo "🔍 Searching both sources for: $QUERY"

RESULTS=$(curl -s "http://localhost:9117/api/v1/books/search?query=$QUERY&limit=5")

# Show statistics
echo "📊 Results:"
echo "$RESULTS" | jq '{
  count: .count,
  total_time_ms: .total_search_time_ms,
  libgen_time_ms: .sources.libgen,
  annas_time_ms: .sources.annas_archive
}'

# Show books with their sources
echo -e "\n📚 Books found:"
echo "$RESULTS" | jq -r '.results[] | "- [\(.source)] \(.title) by \(.author // "Unknown") [\(.extension), \(.size)]"'

# 2. Extract first MD5
MD5=$(echo "$RESULTS" | jq -r '.results[0].md5')
TITLE=$(echo "$RESULTS" | jq -r '.results[0].title')
SOURCE=$(echo "$RESULTS" | jq -r '.results[0].source')

echo -e "\n📥 Selected: $TITLE (from $SOURCE)"
echo "MD5: $MD5"

# 3. Smart download (tries LibGen first, then Anna's)
echo -e "\n⏳ Getting download URL..."
DOWNLOAD=$(curl -s "http://localhost:9117/api/v1/books/download?md5=$MD5")

URL=$(echo "$DOWNLOAD" | jq -r '.download_url')
USED_SOURCE=$(echo "$DOWNLOAD" | jq -r '.source')
CACHED=$(echo "$DOWNLOAD" | jq -r '.cached')

echo "✅ Download ready from: $USED_SOURCE"
echo "🔄 Cached: $CACHED"
echo "🔗 URL: $URL"

# 4. Download the file
FILENAME="$(echo $TITLE | tr ' ' '_' | tr -cd '[:alnum:]_-').$(echo "$RESULTS" | jq -r '.results[0].extension')"
echo -e "\n📥 Downloading to: $FILENAME"

curl -L "$URL" -o "$FILENAME" --progress-bar

echo -e "\n✨ Download complete!"
ls -lh "$FILENAME"
```

**Search specific source:**

```bash
#!/bin/bash

# Search only LibGen (faster, 2-5 seconds)
echo "🔍 Searching LibGen only..."
curl -s "http://localhost:9117/api/v1/books/search/source?query=python&source=libgen&limit=10" | jq .

# Search only Anna's Archive (more comprehensive)
echo "🔍 Searching Anna's Archive only..."
curl -s "http://localhost:9117/api/v1/books/search/source?query=python&source=annas&limit=10" | jq .
```

**Force specific download source:**

```bash
# Force LibGen (fast, but may not have the book)
curl -s "http://localhost:9117/api/v1/books/download?md5=abc123&source=libgen" | jq .

# Force Anna's Archive (slower due to DDOS-Guard, but more comprehensive)
curl -s "http://localhost:9117/api/v1/books/download?md5=abc123&source=annas" | jq .

# Auto mode (smart - tries LibGen first, falls back to Anna's)
curl -s "http://localhost:9117/api/v1/books/download?md5=abc123&source=auto" | jq .
```

**Old method (still works):**

```bash
#!/bin/bash

# 1. Search for a book
QUERY="golang programming"
echo "Searching for: $QUERY"

RESULTS=$(curl -s "http://localhost:9117/api/v1/books/annas/search?query=$QUERY&limit=5")
echo "$RESULTS" | jq .

# 2. Extract first MD5
MD5=$(echo "$RESULTS" | jq -r '.results[0].md5')
echo "Selected MD5: $MD5"

# 3. Get download URL
DOWNLOAD=$(curl -s "http://localhost:9117/api/v1/books/annas/download?md5=$MD5")
URL=$(echo "$DOWNLOAD" | jq -r '.download_url')
TITLE=$(echo "$DOWNLOAD" | jq -r '.title')

echo "Downloading: $TITLE"
echo "URL: $URL"

# 4. Download the file
curl -L "$URL" -o "$(echo $TITLE | tr ' ' '_').epub"
echo "Download complete!"
```

---

## 🔧 Troubleshooting

### Common Issues

#### 1. API Not Responding

**Check if containers are running:**

```bash
docker-compose ps
```

**Check logs:**

```bash
docker-compose logs zil_tor-api
docker-compose logs flaresolverr
```

**Restart services:**

```bash
docker-compose restart
```

#### 2. FlareSolverr Connection Failed

**Error:** `failed to solve challenge: flaresolverr request failed`

**Solution:**

```bash
# Check FlareSolverr is running
docker-compose logs flaresolverr

# Restart FlareSolverr
docker-compose restart flaresolverr

# Verify endpoint
curl http://localhost:8191/
```

#### 3. Indexer Health Check Fails

**Check specific indexer:**

```bash
curl http://localhost:9117/api/v1/health | jq .
```

**Check if sites are accessible:**

```bash
# Test direct access
curl -I https://yts.mx
curl -I https://1337x.to
```

#### 4. Slow Download Solving (Anna's Archive)

This is normal! DDOS-Guard bypass can take 25-45 seconds.

**Response times:**

- LibGen: 2-5 seconds (direct download)
- Anna's Archive: 25-45 seconds (first time, then cached for 30 minutes)

**Check cache status:**

```bash
curl "http://localhost:9117/api/v1/books/annas/download?md5=YOUR_MD5" | jq '.cached'
```

#### 5. Port Already in Use

**Error:** `bind: address already in use`

**Solution:** Change the port in `docker-compose.yml`:

```yaml
ports:
  - "8080:9117" # Change 9117 to any free port
```

#### 6. Memory Issues

**Increase Docker memory limit:**

```bash
# Stop services
docker-compose down

# Edit docker-compose.yml and add:
services:
  zil_tor-api:
    deploy:
      resources:
        limits:
          memory: 2G

# Restart
docker-compose up -d
```

---

## 🔍 Advanced Configuration

### Custom FlareSolverr Settings

Edit `docker-compose.yml`:

```yaml
flaresolverr:
  environment:
    - LOG_LEVEL=debug # More verbose logging
    - CAPTCHA_SOLVER=hcaptcha-solver # If you have captcha solver
    - BROWSER_TIMEOUT=40000 # Increase timeout
```

### Running Without FlareSolverr

You can run with built-in Chrome solver only:

```yaml
environment:
  - SOLVER_ENABLED=true
  - FLARESOLVERR_URL= # Leave empty
```

**Note:** The built-in solver is slower but doesn't require FlareSolverr.

### Custom Chromium Path

If using a custom Chrome/Chromium:

```yaml
environment:
  - CHROME_BIN=/path/to/chrome
```

### Production Deployment

**docker-compose.prod.yml:**

```yaml
services:
  zil_tor-api:
    restart: always
    environment:
      - LOG_LEVEL=warn
      - CACHE_ENABLED=true
    deploy:
      resources:
        limits:
          cpus: "2"
          memory: 2G
        reservations:
          memory: 512M
    healthcheck:
      interval: 60s
      timeout: 10s
      retries: 3
```

Run with:

```bash
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### Reverse Proxy (Nginx)

```nginx
server {
    listen 80;
    server_name api.yourdomain.com;

    location / {
        proxy_pass http://localhost:9117;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        # For long-running requests (DDOS-Guard solving)
        proxy_read_timeout 120s;
        proxy_connect_timeout 120s;
    }
}
```

### Enable HTTPS

Use [Caddy](https://caddyserver.com/) for automatic HTTPS:

```dockerfile
# Caddyfile
api.yourdomain.com {
    reverse_proxy localhost:9117
}
```

---

## 📊 Performance Tips

1. **Keep cache enabled** - Reduces redundant requests by 80%
2. **Use appropriate limits** - Don't request more results than needed
3. **Monitor health** - Set up alerts for failed indexers
4. **Cache download URLs** - Anna's Archive URLs are valid for 30 minutes
5. **Use proxy endpoint** - For Anna's Archive, consider using `/download/proxy` to handle downloads server-side

---

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

---

## 📝 License

[Your License Here]

---

## 🐛 Bug Reports

Found a bug? Please open an issue with:

- Your `docker-compose.yml` configuration
- Relevant logs: `docker-compose logs`
- Steps to reproduce
- Expected vs actual behavior

---

## ⭐ Support

If this project helped you, please give it a star on GitHub!

---

**Made with ❤️ by zilezarach **
