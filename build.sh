#!/bin/bash

# Build script for BitPlay
# This script builds both the frontend (Svelte + CSS) and the Go backend

echo "🔨 Building BitPlay..."
echo ""

# Build Svelte app
echo "📦 Building Svelte components..."
npm run build:svelte
if [ $? -ne 0 ]; then
    echo "❌ Svelte build failed"
    exit 1
fi

# Build CSS
echo "🎨 Building CSS..."
npm run build:css
if [ $? -ne 0 ]; then
    echo "❌ CSS build failed"
    exit 1
fi

# Build Go binary
echo "🔧 Building Go server..."
go build -o bitplay main.go
if [ $? -ne 0 ]; then
    echo "❌ Go build failed"
    exit 1
fi

echo ""
echo "✅ Build complete!"
echo ""
echo "To run BitPlay:"
echo "  ./bitplay"
echo ""
echo "Then open: http://localhost:3347"
