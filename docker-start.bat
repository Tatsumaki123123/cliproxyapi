@echo off
REM CLIProxyAPI Docker 快速启动脚本

echo ========================================
echo CLIProxyAPI Docker 启动脚本
echo ========================================
echo.

REM 检查 Docker 是否运行
docker info >nul 2>&1
if errorlevel 1 (
    echo [错误] Docker 未运行，请先启动 Docker Desktop
    pause
    exit /b 1
)

REM 检查配置文件
if not exist "config.yaml" (
    echo [警告] config.yaml 不存在
    if exist "config.example.yaml" (
        echo [提示] 正在从 config.example.yaml 复制...
        copy config.example.yaml config.yaml
        echo [成功] 已创建 config.yaml，请编辑后重新运行此脚本
        pause
        exit /b 0
    ) else (
        echo [错误] config.example.yaml 也不存在，无法创建配置文件
        pause
        exit /b 1
    )
)

REM 检查 .env 文件
if not exist ".env" (
    echo [警告] .env 不存在
    if exist ".env.example" (
        echo [提示] 正在从 .env.example 复制...
        copy .env.example .env
        echo [成功] 已创建 .env
    )
)

REM 创建必要的目录
if not exist "auths" mkdir auths
if not exist "logs" mkdir logs

echo [信息] 正在启动 Docker Compose...
echo.

REM 构建并启动服务
docker-compose up -d --build

if errorlevel 1 (
    echo.
    echo [错误] 启动失败，请查看上面的错误信息
    pause
    exit /b 1
)

echo.
echo ========================================
echo [成功] 服务已启动！
echo ========================================
echo.
echo 主 API 端点: http://localhost:8317
echo 管理面板:   http://localhost:8317
echo.
echo 查看日志: docker-compose logs -f
echo 停止服务: docker-compose stop
echo 重启服务: docker-compose restart
echo.
echo 正在显示最近的日志（按 Ctrl+C 退出）...
echo ========================================
echo.

timeout /t 3 >nul

docker-compose logs -f --tail=50
