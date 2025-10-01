# Building BitPlay

This guide explains how to build BitPlay from source.

## Prerequisites

- **Node.js** (v18 or higher) and **npm**
- **Go** (v1.21 or higher)

## Build Instructions

### Quick Build (Recommended)

Use the provided build script:

**Linux/macOS:**
```bash
./build.sh
```

**Windows:**
```cmd
build.bat
```

This will:
1. Build the Svelte.js frontend components
2. Compile Tailwind CSS
3. Build the Go server binary

### Manual Build

If you prefer to build manually:

1. **Install Node dependencies:**
   ```bash
   npm install
   ```

2. **Build frontend:**
   ```bash
   npm run build
   ```

3. **Build Go binary:**
   ```bash
   go build -o bitplay main.go
   ```

## Running BitPlay

After building, simply run the binary:

**Linux/macOS:**
```bash
./bitplay
```

**Windows:**
```cmd
bitplay.exe
```

The server will start on `http://localhost:3347`

## Project Structure

```
bitplay/
├── client/           # Static files served by Go
│   ├── assets/       # CSS, JS, images
│   └── index.html    # Main HTML file
├── src/              # Svelte source files
│   ├── components/   # Svelte components
│   ├── App.svelte    # Root Svelte component
│   └── main.js       # Svelte entry point
├── main.go           # Go server
├── build.sh          # Build script (Linux/macOS)
└── build.bat         # Build script (Windows)
```

## Development

For development with hot-reload:

1. **Start Vite dev server (for Svelte):**
   ```bash
   npm run dev
   ```

2. **In another terminal, run Go server:**
   ```bash
   go run main.go
   ```

3. **Watch CSS changes:**
   ```bash
   npm run watch
   ```

## Build Output

- **Go binary:** `bitplay` (or `bitplay.exe` on Windows) - ~22MB
- **Frontend assets:** `client/assets/app.js` and `client/assets/main.css`

## Notes

- The Go binary includes the torrent client and HTTP server
- All frontend assets are served from the `client/` directory
- No need to run npm/node in production - only the Go binary is required
