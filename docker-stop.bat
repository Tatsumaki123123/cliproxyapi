@echo off
REM CLIProxyAPI Docker 停止脚本

echo ========================================
echo CLIProxyAPI Docker 停止脚本
echo ========================================
echo.

docker-compose stop

if errorlevel 1 (
    echo.
    echo [错误] 停止失败
    pause
    exit /b 1
)

echo.
echo [成功] 服务已停止
echo.
echo 重新启动: docker-compose start
echo 或运行: docker-start.bat
echo.
pause
