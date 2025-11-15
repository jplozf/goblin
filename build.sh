#!/bin/bash

# Get the number of git commits
MINOR_VERSION=$(git rev-list --count HEAD)

# Get the short hash of the last git commit
COMMIT_HASH=$(git rev-parse --short HEAD)

# Build the Go application, injecting version information
go build -o goblin -ldflags="-X 'goblin.go/version.Minor=${MINOR_VERSION}' -X 'goblin.go/version.Commit=${COMMIT_HASH}'"

if [ $? -eq 0 ]; then
    echo "Build successful: ./goblin"
else
    echo "Build failed."
fi
