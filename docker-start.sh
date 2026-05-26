#!/bin/bash
# CLIProxyAPI Docker 快速启动脚本 (Linux/Mac)

set -e

echo "========================================"
echo "CLIProxyAPI Docker 启动脚本"
echo "========================================"
echo ""

# 检查 Docker 是否运行
if ! docker info >/dev/null 2>&1; then
    echo "[错误] Docker 未运行，请先启动 Docker"
    exit 1
fi

# 检查 docker-compose 是否安装
if ! command -v docker-compose &> /dev/null; then
    echo "[错误] docker-compose 未安装"
    echo "[提示] 安装方法: sudo apt-get install docker-compose"
    exit 1
fi

# 检查配置文件
if [ ! -f "config.yaml" ]; then
    echo "[警告] config.yaml 不存在"
    if [ -f "config.example.yaml" ]; then
        echo "[提示] 正在从 config.example.yaml 复制..."
        cp config.example.yaml config.yaml
        echo "[成功] 已创建 config.yaml，请编辑后重新运行此脚本"
        echo ""
        echo "必须修改的配置项："
        echo "  1. api-keys: 设置你的 API 密钥"
        echo "  2. remote-management.secret-key: 设置管理密钥"
        echo ""
        exit 0
    else
        echo "[错误] config.example.yaml 也不存在，无法创建配置文件"
        exit 1
    fi
fi

# 检查 .env 文件
if [ ! -f ".env" ]; then
    echo "[警告] .env 不存在"
    if [ -f ".env.example" ]; then
        echo "[提示] 正在从 .env.example 复制..."
        cp .env.example .env
        echo "[成功] 已创建 .env"
    fi
fi

# 创建必要的目录
mkdir -p auths logs

echo "[信息] 正在启动 Docker Compose..."
echo ""

# 构建并启动服务
docker-compose up -d --build

if [ $? -ne 0 ]; then
    echo ""
    echo "[错误] 启动失败，请查看上面的错误信息"
    exit 1
fi

echo ""
echo "========================================"
echo "[成功] 服务已启动！"
echo "========================================"
echo ""
echo "主 API 端点: http://localhost:8317"
echo "管理面板:   http://localhost:8317"
echo ""
echo "查看日志: docker-compose logs -f"
echo "停止服务: docker-compose stop"
echo "重启服务: docker-compose restart"
echo ""
echo "正在显示最近的日志（按 Ctrl+C 退出）..."
echo "========================================"
echo ""

sleep 3

docker-compose logs -f --tail=50
