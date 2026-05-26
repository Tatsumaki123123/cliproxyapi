# Docker 快速参考

## 🚀 一键启动

**Linux/Mac:**
```bash
chmod +x docker-start.sh
./docker-start.sh
```

**Windows:**
```bash
docker-start.bat
```

## 📋 常用命令

```bash
# 启动服务
docker-compose up -d

# 查看日志
docker-compose logs -f

# 停止服务
docker-compose stop

# 重启服务
docker-compose restart

# 进入容器
docker-compose exec cli-proxy-api sh

# 查看状态
docker-compose ps
```

## 🔧 配置文件位置

| 文件 | 路径 | 说明 |
|------|------|------|
| 主配置 | `./config.yaml` | API 密钥、管理密钥等 |
| 环境变量 | `./.env` | Docker 构建参数 |
| Kiro Token | `./auths/*.json` | Kiro 认证文件 |
| 日志 | `./logs/` | 应用日志 |

## 🌐 访问地址

- **主 API**: http://localhost:8317
- **管理面板**: http://localhost:8317
- **健康检查**: http://localhost:8317/health

## 🔐 添加 Kiro 账号

### 方法 1：管理面板（推荐）
1. 访问 http://localhost:8317
2. 登录（使用 config.yaml 中的 secret-key）
3. 进入 OAuth 页面
4. 上传 token 和注册文件

### 方法 2：手动放置

**Linux/Mac:**
```bash
# 复制文件到 auths 目录
cp kiro-token-1.json auths/
cp kiro-registration-1.json auths/

# 重启服务
docker-compose restart
```

**Windows:**
```bash
# 复制文件到 auths 目录
copy kiro-token-1.json auths\
copy kiro-registration-1.json auths\

# 重启服务
docker-compose restart
```

## 🐛 故障排查

### 服务无法启动
```bash
# 查看详细日志
docker-compose logs cli-proxy-api

# 检查端口占用
netstat -ano | findstr "8317"
```

### 配置未生效
```bash
# 确认配置文件内容
docker-compose exec cli-proxy-api cat /CLIProxyAPI/config.yaml

# 重启服务
docker-compose restart
```

### 429 错误（速率限制）
- **解决方案**：添加更多 Kiro 账号（通过管理面板或手动放置）
- 多账号会自动轮询，分散请求压力

## 📊 监控

```bash
# 查看资源使用
docker stats cli-proxy-api

# 查看健康状态
docker-compose ps

# 查看实时日志
docker-compose logs -f --tail=100
```

## 🔄 更新服务

```bash
# 拉取最新代码
git pull

# 重新构建并启动
docker-compose up -d --build
```

## 📖 完整文档

详细配置和高级用法请参考 [DOCKER.md](./DOCKER.md)
