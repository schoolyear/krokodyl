#!/bin/bash

# Test script for krokodyl
# This script ensures the frontend is built before running Go tests

set -e

echo "Building frontend..."
cd frontend
npm install
npm run build
cd ..

echo "Running Go tests..."
go test -v ./...

echo "Tests completed successfully!"