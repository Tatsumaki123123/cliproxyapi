#!/bin/bash
# CLIProxyAPI Docker 停止脚本 (Linux/Mac)

echo "========================================"
echo "CLIProxyAPI Docker 停止脚本"
echo "========================================"
echo ""

docker-compose stop

if [ $? -ne 0 ]; then
    echo ""
    echo "[错误] 停止失败"
    exit 1
fi

echo ""
echo "[成功] 服务已停止"
echo ""
echo "重新启动: docker-compose start"
echo "或运行: ./docker-start.sh"
echo ""
