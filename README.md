# CLIProxyAPI

Go 1.26+ proxy server providing OpenAI/Gemini/Claude/Codex compatible APIs with OAuth and round-robin load balancing.

## 🚀 Quick Start

### Option 1: Docker (Recommended)

```bash
# Windows
docker-start.bat

# Linux/Mac
docker-compose up -d --build
```

See [DOCKER.md](./DOCKER.md) for detailed Docker deployment guide.

### Option 2: Build from Source

```bash
# Build
go build -o cli-proxy-api ./cmd/server

# Run
./cli-proxy-api --config config.yaml
```

## 📚 Documentation

- [Docker Deployment Guide](./DOCKER.md) - Complete Docker Compose setup and usage
- [Agent Development Guide](./AGENTS.md) - Development conventions and architecture
- [Configuration Example](./config.example.yaml) - Full configuration reference

## 🔗 Links

- [GitHub Repository](https://github.com/router-for-me/CLIProxyAPI)
- [Management Panel](https://github.com/router-for-me/Cli-Proxy-API-Management-Center)