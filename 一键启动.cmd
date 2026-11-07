@echo off
setlocal
cd /d "%~dp0"
powershell.exe -NoProfile -File "%~dp0run-ui.ps1"
if errorlevel 1 (
    echo.
    echo Startup failed. Please share the error message above.
    pause
    exit /b 1
)
endlocal
