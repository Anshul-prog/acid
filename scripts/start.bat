@echo off
REM ═══════════════════════════════════════════════════════════════════════════
REM  L.S.D  —  Windows startup script
REM  Double-click this file or run from cmd.exe to start the server.
REM  Prerequisites: Go 1.22+ installed, PostgreSQL running, .env configured.
REM ═══════════════════════════════════════════════════════════════════════════

setlocal

REM ── Configuration ────────────────────────────────────────────────────────────
set APP_NAME=lsd-server
set BUILD_DIR=build
set BINARY=%BUILD_DIR%\%APP_NAME%.exe
set LOG_DIR=logs
set LOG_FILE=%LOG_DIR%\lsd_%date:~-4,4%%date:~-7,2%%date:~-10,2%.log

REM ── Create directories ────────────────────────────────────────────────────────
if not exist %BUILD_DIR% mkdir %BUILD_DIR%
if not exist %LOG_DIR%   mkdir %LOG_DIR%

echo.
echo  ██╗     ███████╗██████╗
echo  ██║     ██╔════╝██╔══██╗
echo  ██║     ███████╗██║  ██║
echo  ██║     ╚════██║██║  ██║
echo  ███████╗███████║██████╔╝
echo  ╚══════╝╚══════╝╚═════╝   Intelligence Platform
echo.

REM ── Load .env if present ─────────────────────────────────────────────────────
if exist .env (
    echo [INFO] Loading environment from .env ...
    for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
        REM Skip comment lines
        if not "%%a"=="" (
            if not "%%a:~0,1%"=="#" set %%a=%%b
        )
    )
    echo [INFO] Environment loaded.
) else (
    echo [WARN] No .env file found. Using system environment variables.
)

REM ── Build ─────────────────────────────────────────────────────────────────────
echo [INFO] Building L.S.D ...
go build -o %BINARY% ./cmd/api
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed. Check the output above.
    pause
    exit /b 1
)
echo [INFO] Build successful: %BINARY%

REM ── Run with auto-restart loop ────────────────────────────────────────────────
echo [INFO] Starting L.S.D server ...
echo [INFO] Press Ctrl+C to stop.
echo.

:loop
echo [%date% %time%] Starting server ... >> %LOG_FILE%
%BINARY% >> %LOG_FILE% 2>&1
set EXIT_CODE=%ERRORLEVEL%
echo [%date% %time%] Server exited with code %EXIT_CODE% >> %LOG_FILE%
if %EXIT_CODE% neq 0 (
    echo [WARN] Server crashed (code %EXIT_CODE%). Restarting in 5 seconds ...
    timeout /t 5 /nobreak >nul
    goto loop
)
echo [INFO] Server stopped cleanly.
pause
endlocal
