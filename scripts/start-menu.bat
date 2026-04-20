@echo off
REM =============================================================================
REM  ACID — Startup Menu (Windows)
REM  Interactive launcher for all server modes.
REM  Usage: Double-click or run: scripts\start-menu.bat
REM =============================================================================
setlocal
echo.
echo ================================================================
echo   ACID  ^|  Advanced Database Interface System
echo   Startup Menu
echo ================================================================
echo.
echo  [1] Backend only          (Go API server on port 8080)
echo  [2] Frontend only         (opens browser, requires backend)
echo  [3] Full stack            (backend + opens browser)
echo  [4] Generate seed data    (populate DB with sample records)
echo  [5] Exit
echo.
set /p CHOICE=Select option (1-5): 

if "%CHOICE%"=="1" call "%~dp0start-backend.bat" & exit /b 0
if "%CHOICE%"=="2" call "%~dp0start-frontend.bat" & exit /b 0
if "%CHOICE%"=="3" call "%~dp0start-fullstack.bat" & exit /b 0
if "%CHOICE%"=="4" call "%~dp0generate-data.bat" & exit /b 0
if "%CHOICE%"=="5" exit /b 0

echo [ERROR] Invalid option. Please enter 1-5.
pause
exit /b 1
