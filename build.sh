#!/bin/bash

# Get the current directory name
current_dir=$(basename "$PWD")

# Build for Windows
echo "Building for Windows..."
GOOS=windows GOARCH=amd64 go build -o "${current_dir}.exe"

# # Build for macOS
# echo "Building for macOS..."
# GOOS=darwin GOARCH=amd64 go build -o "${current_dir}-darwin"

# # Build for macOS Apple Silicon
# echo "Building for macOS Apple Silicon..."
# GOOS=darwin GOARCH=arm64 go build -o "${current_dir}-darwin-arm64"


# # Build for Linux
# echo "Building for Linux..."
# GOOS=linux GOARCH=amd64 go build -o "${current_dir}-linux"

# echo "Build complete! Files are in the dist directory:"
# ls -lh 