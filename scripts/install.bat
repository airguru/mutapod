@echo off
setlocal

set "INSTALL_DIR=%LOCALAPPDATA%\mutapod\bin"
set "BINARY=%~dp0mutapod.exe"

if not exist "%BINARY%" (
    echo Error: mutapod.exe not found next to this script.
    echo Please extract the full zip archive before running install.bat.
    pause
    exit /b 1
)

if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

copy /Y "%BINARY%" "%INSTALL_DIR%\mutapod.exe" >nul
if errorlevel 1 (
    echo Error: failed to copy mutapod.exe to %INSTALL_DIR%
    pause
    exit /b 1
)

:: Add to user PATH without admin rights
powershell -ExecutionPolicy Bypass -Command ^
  "$p = [Environment]::GetEnvironmentVariable('PATH','User');" ^
  "if ($p -notlike '*%INSTALL_DIR%*') {" ^
  "  [Environment]::SetEnvironmentVariable('PATH', $p + ';%INSTALL_DIR%', 'User');" ^
  "  Write-Host 'Added to PATH.' }" ^
  "else { Write-Host 'Already in PATH.' }"

echo.
echo mutapod installed to %INSTALL_DIR%
echo Restart your terminal, then run: mutapod --help
echo.
pause
