@echo off
REM Full stack startup for beginners
REM 1) Starts backend in a separate terminal
REM 2) Waits until backend is healthy
REM 3) Opens frontend in browser

setlocal
set BASE_URL=http://localhost:8080
set MAX_RETRIES=30
set RETRY=0

echo [INFO] Starting backend window ...
start "L.S.D Backend" cmd /c "%~dp0start-backend.bat"

echo [INFO] Waiting for backend health endpoint ...
:wait_loop
set /a RETRY+=1
powershell -NoProfile -Command "try { $r = Invoke-WebRequest -UseBasicParsing '%BASE_URL%/api/health' -TimeoutSec 3; if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 500) { exit 0 } else { exit 1 } } catch { exit 1 }"

if %ERRORLEVEL% equ 0 goto ready
if %RETRY% geq %MAX_RETRIES% goto timeout
timeout /t 2 /nobreak >nul
goto wait_loop

:ready
echo [INFO] Backend is up. Opening frontend ...
start "" "%BASE_URL%/"
exit /b 0

:timeout
echo [WARN] Backend did not become healthy in time.
echo [HINT] Check backend window logs and .env configuration.
exit /b 1
