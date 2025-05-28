@echo off
setlocal

if "%1"=="" goto usage
if "%1"=="install" goto install
if "%1"=="uninstall" goto uninstall
if "%1"=="start" goto start
if "%1"=="stop" goto stop
if "%1"=="status" goto status
goto usage

:install
echo Installing Excel File Finder Service...
finder.exe install
goto end

:uninstall
echo Uninstalling Excel File Finder Service...
finder.exe uninstall
goto end

:start
echo Starting Excel File Finder Service...
finder.exe start
goto end

:stop
echo Stopping Excel File Finder Service...
finder.exe stop
goto end

:status
echo Checking Excel File Finder Service status...
finder.exe status
goto end

:usage
echo Usage: install-service.bat [install^|uninstall^|start^|stop^|status]
echo.
echo Commands:
echo   install    - Install the service
echo   uninstall  - Uninstall the service
echo   start      - Start the service
echo   stop       - Stop the service
echo   status     - Check service status
goto end

:end
endlocal 