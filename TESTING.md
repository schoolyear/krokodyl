# Testing Troubleshooting Guide

## Common Issues and Solutions

### "pattern all:frontend/dist: no matching files found"

**Problem**: Go tests fail with this error because the frontend hasn't been built.

**Solution**: Build the frontend before running tests:
```bash
# Windows
cd frontend && npm install && npm run build && cd .. && go test -v

# Linux/macOS
./test.sh
```

### "npm is not recognized" (Windows)

**Problem**: Node.js/npm is not installed or not in PATH.

**Solution**: 
1. Install Node.js from https://nodejs.org/
2. Restart your terminal/command prompt
3. Verify with `npm --version`

### "go: command not found"

**Problem**: Go is not installed or not in PATH.

**Solution**:
1. Install Go from https://golang.org/dl/
2. Add Go to your PATH
3. Verify with `go version`

### Tests fail with Wails context errors

**Problem**: Some integration tests fail with "invalid context" errors from Wails runtime.

**Solution**: This is expected for tests that try to use Wails runtime functions outside of a running Wails application. These tests verify the error handling works correctly.

### Permission errors on Linux/macOS

**Problem**: Test script `./test.sh` fails with "Permission denied"

**Solution**: Make the script executable:
```bash
chmod +x test.sh
```

## Quick Test Commands

**Full test with frontend build:**
```bash
# Linux/macOS
./test.sh

# Windows
test.bat
```

**Go tests only (requires frontend already built):**
```bash
go test -v ./...
```

**Test with coverage:**
```bash
go test -v -cover ./...
```