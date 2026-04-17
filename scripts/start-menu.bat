@echo off
REM Single-view launcher for beginners

setlocal
echo.
echo ================================
echo   L.S.D Startup Menu
echo ================================
echo [1] Backend only
echo [2] Frontend only (requires backend)
echo [3] Full stack (backend + frontend)
echo [4] Exit
echo.
set /p CHOICE=Select option (1-4): 

if "%CHOICE%"=="1" call "%~dp0start-backend.bat" & exit /b 0
if "%CHOICE%"=="2" call "%~dp0start-frontend.bat" & exit /b 0
if "%CHOICE%"=="3" call "%~dp0start-fullstack.bat" & exit /b 0
if "%CHOICE%"=="4" exit /b 0

echo [ERROR] Invalid option.
exit /b 1
