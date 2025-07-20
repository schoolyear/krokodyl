@echo off
REM Test script for krokodyl on Windows
REM This script ensures the frontend is built before running Go tests

echo Building frontend...
cd frontend
call npm install
if %errorlevel% neq 0 (
    echo Error: npm install failed
    exit /b 1
)

call npm run build
if %errorlevel% neq 0 (
    echo Error: npm run build failed
    exit /b 1
)

cd ..

echo Running Go tests...
go test -v ./...
if %errorlevel% neq 0 (
    echo Error: Go tests failed
    exit /b 1
)

echo Tests completed successfully!