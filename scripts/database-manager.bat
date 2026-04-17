@echo off
REM Database workspace manager (Windows)
REM Supports listing SQL files and applying one/all files using DATABASE_URL.

setlocal EnableExtensions EnableDelayedExpansion
set ROOT=%~dp0..
set CMD=%~1
set ARG=%~2

pushd "%ROOT%" >nul
call :load_env

if "%CMD%"=="" goto usage

where psql >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo [ERROR] psql is not installed or not in PATH.
    popd >nul
    exit /b 1
)

if "%DATABASE_URL%"=="" (
    echo [ERROR] DATABASE_URL is not set.
    echo [HINT] Set DATABASE_URL in .env or your terminal.
    popd >nul
    exit /b 1
)

if /I "%CMD%"=="list" goto list
if /I "%CMD%"=="apply" goto apply
if /I "%CMD%"=="apply-all" goto apply_all
goto usage

:list
echo [INFO] SQL files in /databases:
dir /b /s "databases\*.sql" 2>nul
if %ERRORLEVEL% neq 0 echo [INFO] No SQL files found yet.
popd >nul
exit /b 0

:apply
if "%ARG%"=="" (
    echo [ERROR] Missing SQL file path.
    echo Example: scripts\database-manager.bat apply databases\migrations\001_example.sql
    popd >nul
    exit /b 1
)
if not exist "%ARG%" (
    echo [ERROR] File not found: %ARG%
    popd >nul
    exit /b 1
)
echo [INFO] Applying %ARG% ...
psql "%DATABASE_URL%" -v ON_ERROR_STOP=1 -f "%ARG%"
set EXIT_CODE=%ERRORLEVEL%
popd >nul
exit /b %EXIT_CODE%

:apply_all
set TARGET=%ARG%
if "%TARGET%"=="" set TARGET=migrations
if not exist "databases\%TARGET%" (
    echo [ERROR] Folder not found: databases\%TARGET%
    popd >nul
    exit /b 1
)

echo [INFO] Applying all SQL files from databases\%TARGET% ...
set APPLIED=0
for /f "delims=" %%F in ('dir /b /on "databases\%TARGET%\*.sql" 2^>nul') do (
    set APPLIED=1
    echo [INFO] Applying databases\%TARGET%\%%F
    psql "%DATABASE_URL%" -v ON_ERROR_STOP=1 -f "databases\%TARGET%\%%F"
    if !ERRORLEVEL! neq 0 (
        echo [ERROR] Failed on databases\%TARGET%\%%F
        popd >nul
        exit /b 1
    )
)
if "%APPLIED%"=="0" echo [INFO] No SQL files to apply in databases\%TARGET%
popd >nul
exit /b 0

:usage
echo Usage:
echo   scripts\database-manager.bat list
echo   scripts\database-manager.bat apply databases\migrations\001_example.sql
echo   scripts\database-manager.bat apply-all [migrations^|seeds^|incoming]
popd >nul
exit /b 1

:load_env
if exist ".env" (
    for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
        if not "%%a"=="" if not "%%a:~0,1%"=="#" set %%a=%%b
    )
)
exit /b 0
