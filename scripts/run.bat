@echo off
REM =============================================================================
REM  ACID — Windows Run Script
REM  Builds the Go binary and starts the server with auto-restart.
REM
REM  Usage:
REM    Double-click this file  OR  run from cmd:  scripts\run.bat
REM
REM  Prerequisites:
REM    - Go 1.22+ installed and on PATH
REM    - .env file configured (copy from .env.example)
REM    - PostgreSQL accessible at DATABASE_URL in .env
REM =============================================================================
setlocal EnableDelayedExpansion

REM ── Configuration ─────────────────────────────────────────────────────────────
set APP_NAME=acid-server
set BUILD_DIR=build
set BINARY=%BUILD_DIR%\%APP_NAME%.exe
set LOG_DIR=logs

REM ── Create directories ────────────────────────────────────────────────────────
if not exist "%BUILD_DIR%" mkdir "%BUILD_DIR%"
if not exist "%LOG_DIR%"   mkdir "%LOG_DIR%"

REM ── Generate a locale-safe date for log filename ──────────────────────────────
for /f %%a in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd"') do set LOG_DATE=%%a
set LOG_FILE=%LOG_DIR%\acid_%LOG_DATE%.log

REM ── Banner ────────────────────────────────────────────────────────────────────
echo.
echo  =========================================================
echo   ACID  ^|  Advanced Database Interface System
echo  =========================================================
echo.

REM ── Check Go is available ─────────────────────────────────────────────────────
where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go is not installed or not on PATH.
    echo         Download from https://go.dev/dl/
    pause
    exit /b 1
)
for /f "tokens=*" %%v in ('go version') do echo [INFO] %%v

REM ── Load .env file ────────────────────────────────────────────────────────────
if exist .env (
    echo [INFO] Loading environment from .env ...
    for /f "usebackq eol=# tokens=1,* delims==" %%a in (".env") do (
        if not "%%a"=="" (
            REM Skip lines starting with # and blank keys
            set "TRIMMED=%%a"
            if not "!TRIMMED:~0,1!"=="#" (
                set "%%a=%%b"
            )
        )
    )
    echo [INFO] Environment loaded.
) else (
    echo [WARN] No .env file found. Copy .env.example to .env and configure it.
    echo        Using system environment variables only.
)

REM ── Validate required env vars ────────────────────────────────────────────────
if "%DATABASE_URL%"=="" (
    echo [ERROR] DATABASE_URL is not set. Please configure .env
    pause
    exit /b 1
)
echo [INFO] Database: %DATABASE_URL:~0,50%...

REM ── Build ─────────────────────────────────────────────────────────────────────
echo.
echo [INFO] Building ACID server...
go build -o "%BINARY%" .\cmd\api
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed. Check the errors above.
    pause
    exit /b 1
)
echo [INFO] Build successful: %BINARY%

REM ── Run with auto-restart loop ────────────────────────────────────────────────
echo.
echo [INFO] Starting ACID server...
echo [INFO] Press Ctrl+C to stop.
echo [INFO] Logs: %LOG_FILE%
echo.
echo  ---------------------------------------------------------
echo   Admin Panel:   http://localhost:8080/admin
echo   Home Page:     http://localhost:8080/
echo   API Docs:      http://localhost:8080/docs
echo   Health Check:  http://localhost:8080/api/health
echo  ---------------------------------------------------------
echo.

:loop
echo [%date% %time%] Starting ACID server... >> "%LOG_FILE%"
"%BINARY%" >> "%LOG_FILE%" 2>&1
set EXIT_CODE=%ERRORLEVEL%
echo [%date% %time%] Server exited with code %EXIT_CODE% >> "%LOG_FILE%"

if %EXIT_CODE% equ 0 (
    echo [INFO] Server stopped cleanly.
    goto :done
)

echo [WARN] Server crashed (exit code %EXIT_CODE%). Restarting in 5 seconds...
echo [WARN] Check %LOG_FILE% for details.
timeout /t 5 /nobreak >nul
goto loop

:done
pause
endlocal
