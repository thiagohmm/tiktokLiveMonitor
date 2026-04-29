#!/bin/bash

# Script to build TikTok Live Monitor for multiple platforms
# Targets: Windows (x64, arm64), Linux (x64, arm64), Mac (arm64)

echo "🚀 Starting multi-platform build process..."

# Ensure dependencies are installed
if [ ! -d "node_modules" ]; then
    echo "📦 Installing dependencies..."
    npm install
fi

# Function to run build
build_platform() {
    PLATFORM=$1
    echo "🛠 Building for $PLATFORM..."
    npm run "build:$PLATFORM"
    if [ $? -eq 0 ]; then
        echo "✅ $PLATFORM build successful!"
    else
        echo "❌ $PLATFORM build failed."
        if [ "$PLATFORM" == "mac" ]; then
            echo "ℹ️  Note: macOS builds usually require a macOS host."
        fi
    fi
}

# Build Windows
build_platform "win"

# Build Linux
build_platform "linux"

# Build Mac
# Note: This might fail on Linux/Windows without specialized tools like remote build server or osxcross
build_platform "mac"

echo "🏁 Build process finished! Check the 'dist' folder for artifacts."
