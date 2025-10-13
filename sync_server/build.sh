#!/bin/bash

echo "Building sync_server for multiple platforms..."
echo "================================================"

# Get version from git or use default
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "v1.0.0")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

echo "Version: $VERSION"
echo "Build Time: $BUILD_TIME"
echo ""

# Create dist directory
mkdir -p dist

# Build for Linux AMD64
echo "Building for Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -o dist/sync_server-linux-amd64 main.go
if [ $? -eq 0 ]; then
    echo "✓ Linux AMD64 build successful"
    ls -lh dist/sync_server-linux-amd64
else
    echo "✗ Linux AMD64 build failed"
fi
echo ""

# Build for Windows AMD64
echo "Building for Windows AMD64..."
GOOS=windows GOARCH=amd64 go build -o dist/sync_server-windows-amd64.exe main.go
if [ $? -eq 0 ]; then
    echo "✓ Windows AMD64 build successful"
    ls -lh dist/sync_server-windows-amd64.exe
else
    echo "✗ Windows AMD64 build failed"
fi
echo ""

# Build for macOS AMD64 (Intel)
echo "Building for macOS AMD64 (Intel)..."
GOOS=darwin GOARCH=amd64 go build -o dist/sync_server-mac-amd64 main.go
if [ $? -eq 0 ]; then
    echo "✓ macOS AMD64 build successful"
    ls -lh dist/sync_server-mac-amd64
else
    echo "✗ macOS AMD64 build failed"
fi
echo ""

# Build for macOS ARM64 (Apple Silicon)
echo "Building for macOS ARM64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o dist/sync_server-mac-arm64 main.go
if [ $? -eq 0 ]; then
    echo "✓ macOS ARM64 build successful"
    ls -lh dist/sync_server-mac-arm64
else
    echo "✗ macOS ARM64 build failed"
fi
echo ""

# Create Universal macOS binary
echo "Creating Universal macOS binary..."
if [ -f dist/sync_server-mac-amd64 ] && [ -f dist/sync_server-mac-arm64 ]; then
    lipo -create -output dist/sync_server-mac-universal dist/sync_server-mac-amd64 dist/sync_server-mac-arm64
    if [ $? -eq 0 ]; then
        echo "✓ Universal macOS build successful"
        ls -lh dist/sync_server-mac-universal
        # Verify it's universal
        echo "Architecture check:"
        lipo -info dist/sync_server-mac-universal
    else
        echo "✗ Universal macOS build failed"
    fi
else
    echo "✗ Cannot create universal binary - missing Intel or ARM builds"
fi
echo ""

echo "================================================"
echo "Build Summary:"
echo "================================================"
ls -lh dist/
echo ""
echo "All builds completed!"
