# Docker Compose 部署指南

## 📦 快速开始

### 1. 准备配置文件

```bash
# Linux/Mac
cp config.example.yaml config.yaml

# Windows
copy config.example.yaml config.yaml

# 编辑配置文件，至少修改以下内容：
# - api-keys: 添加你的 API 密钥
# - remote-management.secret-key: 设置管理密钥
# - 添加 Kiro 账号配置（通过管理面板或手动配置）
```

### 2. 准备认证文件目录

```bash
# 创建 auths 目录（如果不存在）
mkdir -p auths

# 如果你有 Kiro token 文件，放到 auths 目录下
# 例如：auths/kiro-token-1.json, auths/kiro-registration-1.json
```

### 3. 启动服务

**Linux/Mac:**
```bash
# 一键启动（推荐）
chmod +x docker-start.sh
./docker-start.sh

# 或手动启动
docker-compose up -d --build
```

**Windows:**
```bash
# 一键启动（推荐）
docker-start.bat

# 或手动启动
docker-compose up -d --build
```

**查看日志:**
```bash
# 查看日志
docker-compose logs -f

# 查看实时日志（最近 100 行）
docker-compose logs -f --tail=100
```

### 4. 验证服务

```bash
# 检查服务状态
docker-compose ps

# 测试 API 端点
curl http://localhost:8317/health

# 访问管理面板
# 浏览器打开: http://localhost:8317
```

## 🔧 配置说明

### 环境变量（.env 文件）

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `CLI_PROXY_IMAGE` | Docker 镜像名称 | `cli-proxy-api:local` |
| `VERSION` | 版本号 | `dev` |
| `CLI_PROXY_CONFIG_PATH` | 配置文件路径 | `./config.yaml` |
| `CLI_PROXY_AUTH_PATH` | 认证文件目录 | `./auths` |
| `CLI_PROXY_LOG_PATH` | 日志目录 | `./logs` |
| `DEPLOY` | 部署模式 | `production` |
| `TZ` | 时区 | `Asia/Shanghai` |

### 端口映射

| 容器端口 | 主机端口 | 用途 |
|---------|---------|------|
| 8317 | 8317 | **主 API 端口**（必需） |
| 8085 | 8085 | 管理面板（可选） |
| 1455 | 1455 | OAuth 回调（可选） |
| 54545 | 54545 | 额外端口（可选） |
| 51121 | 51121 | 额外端口（可选） |
| 11451 | 11451 | 额外端口（可选） |

**注意**：如果不需要某些端口，可以在 `docker-compose.yml` 中注释掉。

### 卷挂载

| 主机路径 | 容器路径 | 权限 | 说明 |
|---------|---------|------|------|
| `./config.yaml` | `/CLIProxyAPI/config.yaml` | ro | 配置文件（只读） |
| `./auths` | `/root/.cli-proxy-api` | rw | 认证文件目录 |
| `./logs` | `/CLIProxyAPI/logs` | rw | 日志目录 |

## 📝 常用命令

### 服务管理

```bash
# 启动服务
docker-compose up -d

# 停止服务
docker-compose stop

# 重启服务
docker-compose restart

# 停止并删除容器
docker-compose down

# 停止并删除容器、网络、卷
docker-compose down -v
```

### 日志查看

```bash
# 查看所有日志
docker-compose logs

# 实时查看日志
docker-compose logs -f

# 查看最近 100 行日志
docker-compose logs --tail=100

# 查看特定时间的日志
docker-compose logs --since 2026-05-09T10:00:00
```

### 容器管理

```bash
# 进入容器
docker-compose exec cli-proxy-api sh

# 查看容器状态
docker-compose ps

# 查看容器资源使用
docker stats cli-proxy-api

# 重新构建镜像
docker-compose build --no-cache
```

### 配置热重载

```bash
# 修改 config.yaml 后，服务会自动重载配置（无需重启）
# 如果需要强制重启：
docker-compose restart
```

## 🔐 添加 Kiro 账号

### 方法 1：通过管理面板（推荐）

1. 访问管理面板：`http://localhost:8317`
2. 使用 `config.yaml` 中的 `remote-management.secret-key` 登录
3. 进入 **OAuth** 页面
4. 上传 Kiro token 文件和注册文件

### 方法 2：手动放置文件

**Linux/Mac:**
```bash
# 将 Kiro token 文件放到 auths 目录
cp kiro-token-1.json auths/
cp kiro-registration-1.json auths/

# 重启服务以加载新文件
docker-compose restart
```

**Windows:**
```bash
# 将 Kiro token 文件放到 auths 目录
copy kiro-token-1.json auths\
copy kiro-registration-1.json auths\

# 重启服务以加载新文件
docker-compose restart
```

### 方法 3：使用 kiro-cli 导入（容器内）

```bash
# 进入容器
docker-compose exec cli-proxy-api sh

# 运行 kiro-cli 登录（如果容器内安装了 kiro-cli）
kiro-cli login

# 退出容器
exit

# 重启服务
docker-compose restart
```

## 🚀 生产环境建议

### 1. 使用外部配置管理

```yaml
# docker-compose.yml
environment:
  # 使用 Postgres 存储
  PGSTORE_DSN: postgresql://user:pass@postgres:5432/cliproxy
  
  # 或使用 Git 存储
  GITSTORE_GIT_URL: https://github.com/your-org/cli-proxy-config.git
  GITSTORE_GIT_TOKEN: ${GITSTORE_GIT_TOKEN}
```

### 2. 配置反向代理（Nginx/Caddy）

```nginx
# Nginx 示例
server {
    listen 443 ssl http2;
    server_name api.example.com;
    
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    
    location / {
        proxy_pass http://localhost:8317;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 超时设置（重要！）
        proxy_connect_timeout 60s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;
    }
}
```

### 3. 限制管理面板访问

```yaml
# config.yaml
remote-management:
  allow-remote: false  # 仅允许 localhost 访问
  secret-key: "$2a$10$..."  # 使用强密码
```

### 4. 配置日志轮转

```yaml
# config.yaml
logging-to-file: true
logs-max-total-size-mb: 1024  # 限制日志总大小为 1GB
error-logs-max-files: 10
```

### 5. 启用健康检查

```bash
# 健康检查已在 docker-compose.yml 中配置
# 可以通过以下命令查看健康状态：
docker-compose ps
```

## 🐛 故障排查

### 服务无法启动

```bash
# 查看详细日志
docker-compose logs cli-proxy-api

# 检查配置文件语法
docker-compose config

# 检查端口占用
netstat -ano | findstr "8317"
```

### 配置文件未生效

```bash
# 确认配置文件挂载正确
docker-compose exec cli-proxy-api cat /CLIProxyAPI/config.yaml

# 重启服务
docker-compose restart
```

### Kiro 账号无法加载

```bash
# 检查 auths 目录挂载
docker-compose exec cli-proxy-api ls -la /root/.cli-proxy-api

# 查看日志中的认证错误
docker-compose logs cli-proxy-api | findstr "kiro"
```

### 请求速率限制（429 错误）

```bash
# 添加更多 Kiro 账号（参考上面的"添加 Kiro 账号"部分）
# 或调整重试配置：

# config.yaml
request-retry: 3
max-retry-interval: 60
max-retry-credentials: 0  # 尝试所有可用账号
```

### 容器内存/CPU 占用过高

```bash
# 查看资源使用
docker stats cli-proxy-api

# 启用商业模式（减少内存占用）
# config.yaml
commercial-mode: true
```

## 📊 监控和维护

### 查看使用统计

```bash
# 访问管理面板查看使用统计
# http://localhost:8317

# 或通过 API 查询
curl -H "Authorization: Bearer YOUR_MANAGEMENT_KEY" \
  http://localhost:8317/v0/management/usage
```

### 备份配置和数据

**Linux/Mac:**
```bash
# 备份配置文件
cp config.yaml config.yaml.backup

# 备份认证文件
cp -r auths auths.backup

# 备份日志
cp -r logs logs.backup
```

**Windows:**
```bash
# 备份配置文件
copy config.yaml config.yaml.backup

# 备份认证文件
xcopy auths auths.backup /E /I

# 备份日志
xcopy logs logs.backup /E /I
```

### 更新服务

```bash
# 拉取最新代码
git pull

# 重新构建并启动
docker-compose up -d --build

# 查看日志确认启动成功
docker-compose logs -f --tail=100
```

## 🔗 相关链接

- [项目 GitHub](https://github.com/router-for-me/CLIProxyAPI)
- [管理面板](https://github.com/router-for-me/Cli-Proxy-API-Management-Center)
- [配置文件示例](./config.example.yaml)
- [环境变量示例](./.env.example)

## 💡 提示

1. **首次运行**：确保 `config.yaml` 中至少配置了 `api-keys` 和 `remote-management.secret-key`
2. **多账号轮询**：添加多个 Kiro 账号可以有效避免速率限制（429 错误）
3. **配置热重载**：修改 `config.yaml` 后无需重启，服务会自动重载
4. **日志查看**：使用 `docker-compose logs -f` 实时查看日志，方便调试
5. **健康检查**：服务启动后约 40 秒会进行首次健康检查
6. **Windows 路径**：在 `.env` 中使用相对路径（`./config.yaml`）或绝对路径（`D:\path\to\config.yaml`）
