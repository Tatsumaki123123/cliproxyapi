# Linux 服务器快速启动指南

## 🚀 快速启动（3 步）

### 1. 准备配置文件

```bash
# 复制配置文件
cp config.example.yaml config.yaml

# 编辑配置（重要！）
nano config.yaml
# 或使用 vim
vim config.yaml
```

**必须修改的配置项：**
```yaml
# 1. 修改监听地址（允许外部访问）
host: "0.0.0.0"  # 改为 0.0.0.0 或留空

# 2. 设置 API 密钥
api-keys:
  - sk-your-custom-api-key-here

# 3. 设置管理密钥（用于登录管理面板）
remote-management:
  allow-remote: true
  secret-key: "your-strong-password-here"  # 首次启动会自动加密
```

### 2. 启动服务

```bash
# 方法 1：一键启动（推荐）
chmod +x docker-start.sh
./docker-start.sh

# 方法 2：手动启动
docker-compose up -d --build

# 查看日志
docker-compose logs -f
```

### 3. 验证服务

```bash
# 检查服务状态
docker-compose ps

# 测试 API（本地）
curl http://localhost:8317/health

# 测试 API（外部访问，替换为你的服务器 IP）
curl http://YOUR_SERVER_IP:8317/health
```

## 🔐 添加 Kiro 账号

### 方法 1：通过管理面板（推荐）

1. 浏览器访问：`http://YOUR_SERVER_IP:8317`
2. 使用 `config.yaml` 中的 `secret-key` 登录
3. 进入 **OAuth** 页面
4. 上传 Kiro token 和注册文件

### 方法 2：手动上传文件

```bash
# 从本地上传到服务器（在本地电脑运行）
scp kiro-token-1.json root@YOUR_SERVER_IP:/www/wwwroot/202604/CliProxyAPI/auths/
scp kiro-registration-1.json root@YOUR_SERVER_IP:/www/wwwroot/202604/CliProxyAPI/auths/

# 在服务器上重启服务
docker-compose restart
```

### 方法 3：使用 SFTP 工具

使用 WinSCP、FileZilla 等工具：
1. 连接到服务器
2. 上传文件到 `/www/wwwroot/202604/CliProxyAPI/auths/`
3. 运行 `docker-compose restart`

## 📋 常用命令

```bash
# 查看日志
docker-compose logs -f

# 查看最近 100 行日志
docker-compose logs -f --tail=100

# 停止服务
docker-compose stop

# 启动服务
docker-compose start

# 重启服务
docker-compose restart

# 查看服务状态
docker-compose ps

# 进入容器
docker-compose exec cli-proxy-api sh

# 查看资源使用
docker stats cli-proxy-api
```

## 🔥 防火墙配置

如果无法从外部访问，需要开放端口：

### Ubuntu/Debian (ufw)
```bash
# 开放主端口
sudo ufw allow 8317/tcp

# 查看状态
sudo ufw status
```

### CentOS/RHEL (firewalld)
```bash
# 开放主端口
sudo firewall-cmd --permanent --add-port=8317/tcp
sudo firewall-cmd --reload

# 查看状态
sudo firewall-cmd --list-ports
```

### 云服务器安全组

如果使用阿里云、腾讯云、AWS 等，还需要在控制台配置安全组：
- 添加入站规则
- 协议：TCP
- 端口：8317
- 来源：0.0.0.0/0（或指定 IP）

## 🌐 Nginx 反向代理（可选）

如果想使用域名访问，可以配置 Nginx：

```nginx
# /etc/nginx/sites-available/cliproxy
server {
    listen 80;
    server_name api.yourdomain.com;
    
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

启用配置：
```bash
sudo ln -s /etc/nginx/sites-available/cliproxy /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

## 🐛 故障排查

### 1. Docker 未运行
```bash
# 启动 Docker
sudo systemctl start docker

# 设置开机自启
sudo systemctl enable docker
```

### 2. 端口被占用
```bash
# 查看端口占用
sudo netstat -tlnp | grep 8317

# 或使用 ss
sudo ss -tlnp | grep 8317

# 杀死占用进程
sudo kill -9 <PID>
```

### 3. 权限问题
```bash
# 确保当前用户在 docker 组
sudo usermod -aG docker $USER

# 重新登录或运行
newgrp docker
```

### 4. 配置文件错误
```bash
# 验证 YAML 语法
docker-compose config

# 查看容器内的配置
docker-compose exec cli-proxy-api cat /CLIProxyAPI/config.yaml
```

### 5. 无法访问管理面板
```bash
# 检查 config.yaml 中的配置
host: "0.0.0.0"  # 必须是 0.0.0.0 或留空，不能是 127.0.0.1

remote-management:
  allow-remote: true  # 必须是 true
```

### 6. Kiro 账号无法加载
```bash
# 检查 auths 目录权限
ls -la auths/

# 查看容器内的文件
docker-compose exec cli-proxy-api ls -la /root/.cli-proxy-api/

# 查看日志中的错误
docker-compose logs cli-proxy-api | grep -i kiro
docker-compose logs cli-proxy-api | grep -i error
```

## 📊 监控和维护

### 查看服务状态
```bash
# 健康检查
curl http://localhost:8317/health

# 查看容器状态
docker-compose ps

# 查看资源使用
docker stats cli-proxy-api
```

### 日志管理
```bash
# 查看日志大小
du -sh logs/

# 清理旧日志（如果日志太大）
rm -rf logs/*.log

# 重启服务
docker-compose restart
```

### 备份
```bash
# 备份配置和认证文件
tar -czf cliproxy-backup-$(date +%Y%m%d).tar.gz config.yaml auths/

# 恢复备份
tar -xzf cliproxy-backup-20260509.tar.gz
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

## 🎯 性能优化

### 1. 启用商业模式（减少内存占用）
```yaml
# config.yaml
commercial-mode: true
```

### 2. 限制日志大小
```yaml
# config.yaml
logging-to-file: true
logs-max-total-size-mb: 1024  # 限制为 1GB
```

### 3. 添加多个 Kiro 账号（避免 429 限流）
- 通过管理面板上传多个 token 文件
- 系统会自动轮询，分散请求压力

## 🔗 访问地址

- **主 API**: `http://YOUR_SERVER_IP:8317`
- **管理面板**: `http://YOUR_SERVER_IP:8317`
- **健康检查**: `http://YOUR_SERVER_IP:8317/health`

## 📖 完整文档

- [Docker 完整指南](./DOCKER.md)
- [快速参考](./DOCKER-QUICKREF.md)
- [开发规范](./AGENTS.md)
