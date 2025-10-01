@echo off
REM Build script for BitPlay (Windows)
REM This script builds both the frontend (Svelte + CSS) and the Go backend

echo Building BitPlay...
echo.

REM Build Svelte app
echo Building Svelte components...
call npm run build:svelte
if %errorlevel% neq 0 (
    echo Svelte build failed
    exit /b 1
)

REM Build CSS
echo Building CSS...
call npm run build:css
if %errorlevel% neq 0 (
    echo CSS build failed
    exit /b 1
)

REM Build Go binary
echo Building Go server...
go build -o bitplay.exe main.go
if %errorlevel% neq 0 (
    echo Go build failed
    exit /b 1
)

echo.
echo Build complete!
echo.
echo To run BitPlay:
echo   bitplay.exe
echo.
echo Then open: http://localhost:3347
