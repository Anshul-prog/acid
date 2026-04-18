@echo off
REM Frontend entrypoint for beginners
REM Frontend is served by backend from http://localhost:8080

setlocal
set BASE_URL=http://localhost:8080

echo [INFO] Checking backend at %BASE_URL%/api/health ...
powershell -NoProfile -Command "try { $r = Invoke-WebRequest -UseBasicParsing '%BASE_URL%/api/health' -TimeoutSec 3; if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 300) { exit 0 } else { exit 1 } } catch { exit 1 }"

if %ERRORLEVEL% neq 0 (
    echo [ERROR] Backend is not running.
    echo [HINT] Start backend first with scripts\start-backend.bat
    pause
    exit /b 1
)

echo [INFO] Opening frontend in browser ...
start "" "%BASE_URL%/"
exit /b 0
