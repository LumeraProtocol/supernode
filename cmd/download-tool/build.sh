#!/bin/bash
set -e

echo "🔨 Building Supernode Download Tool..."

# Build for current platform
go build -o download-tool .

echo "✅ Build complete!"
echo ""
echo "Usage examples:"
echo "  ./download-tool -action abc123"
echo "  ./download-tool -action abc123 -endpoint localhost:9090 -out myfile.dat"
echo "  ./download-tool -help"
echo ""

# Make it executable
chmod +x download-tool

echo "📦 Tool ready: ./download-tool"