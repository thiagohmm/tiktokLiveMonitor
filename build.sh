#!/bin/bash

# Script to build TikTok Live Monitor for a specific platform
# Usage: ./build.sh <os> <arch>
# Example: ./build.sh win x64
# Example: ./build.sh mac arm64

OS=$1
ARCH=$2

if [ -z "$OS" ] || [ -z "$ARCH" ]; then
    echo "❌ Usage: ./build.sh <os> <arch>"
    echo "   OS: win | mac | linux"
    echo "   Arch: x64 | arm64 (Pi/Raspberry 64-bit = linux arm64; Windows ARM Surface = win arm64 via npm run setup-llm -- win arm64)"
    echo "   Modo só navegador (qualquer SO com Node): HOST=0.0.0.0 npm run start:web"
    exit 1
fi

echo "🚀 Preparing build for $OS ($ARCH)..."

# Ensure dependencies are installed
if [ ! -d "node_modules" ]; then
    echo "📦 Installing dependencies..."
    npm install
fi

# Run LLM setup for the target platform
echo "🤖 Setting up LLM for $OS ($ARCH)..."
npm run setup-llm -- "$OS" "$ARCH"

if [ $? -ne 0 ]; then
    echo "❌ LLM setup failed."
    exit 1
fi

# Run build
echo "🛠 Building for $OS $ARCH..."
# electron-builder costuma exigir build nativo (ex.: Linux ARM no próprio Linux ARM).
npx electron-builder --"$OS" --"$ARCH"

if [ $? -eq 0 ]; then
    echo "✅ Build successful! Check the 'dist' folder."
else
    echo "❌ Build failed."
    echo "   Dica: gere o instalável no mesmo SO/arquitetura alvo, ou use CI. Para Raspberry/navegador: só Node + npm run start:web."
fi
