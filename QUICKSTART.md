# 🚀 快速开始指南

## 📋 前置要求

- Go 1.21+
- Docker Desktop
- Git

## 🎯 5分钟启动

### 1. 克隆项目（如果还没有）

```bash
cd /Users/wepie/Desktop/AI\ Agent/
git clone <your-repo-url> AI-Agent-Arrange
cd AI-Agent-Arrange
```

### 2. 配置环境变量

```bash
# 已经创建好 .env 文件
# 检查并修改配置（特别是密码和 API Keys）
cat .env
```

### 3. 启动基础服务

```bash
# 方式1: 使用启动脚本（推荐）
./scripts/start-dev.sh

# 方式2: 手动启动
docker-compose -f deployments/docker-compose-minimal.yml --env-file .env up -d
```

### 4. 启动 Go 应用

```bash
# 下载依赖（首次运行）
go mod download

# 启动服务器
go run cmd/server/main.go
```

## ✅ 验证安装

### 检查 Docker 服务

```bash
docker ps
```

应该看到 3 个运行中的容器：
- ✅ ai-agent-redis
- ✅ ai-agent-mysql
- ✅ ai-agent-rabbitmq

### 访问 Web 管理界面

- **RabbitMQ**: http://localhost:15672 (guest/guest)

### 测试 Go 应用

访问 http://localhost:8080 应该看到服务正在运行。

## 📁 项目结构

```
AI-Agent-Arrange/
├── .env                    # 环境变量（不提交到 Git）
├── .env.example            # 环境变量模板
├── cmd/                    # 应用入口
│   └── server/main.go      # 主服务器
├── internal/               # 私有代码
│   ├── agent/              # Agent 核心
│   ├── orchestrator/       # 编排引擎
│   └── messaging/          # 消息队列
├── pkg/                    # 公共库
│   ├── config/             # 配置管理
│   └── logger/             # 日志系统
├── configs/                # 配置文件
│   ├── config.yaml         # 主配置
│   ├── agents.yaml         # Agent 定义
│   └── tools.yaml          # 工具定义
├── deployments/            # 部署配置
│   ├── docker-compose-minimal.yml  # 最小开发环境
│   └── docker-compose.yml          # 完整环境
└── docs/                   # 文档
    └── ENV_CONFIG.md       # 环境变量配置指南
```

## 🔧 常用命令

### Docker 服务管理

```bash
# 启动服务
docker-compose -f deployments/docker-compose-minimal.yml --env-file .env up -d

# 停止服务
docker-compose -f deployments/docker-compose-minimal.yml down

# 查看日志
docker-compose -f deployments/docker-compose-minimal.yml logs -f

# 重启服务
docker-compose -f deployments/docker-compose-minimal.yml restart
```

### Go 应用

```bash
# 开发模式（热重载推荐使用 air）
go run cmd/server/main.go

# 编译
make build

# 运行测试
make test

# 查看帮助
make help
```

### 数据库操作

```bash
# 连接 MySQL
docker exec -it ai-agent-mysql mysql -uroot -p${MYSQL_PASSWORD}

# 运行迁移
make migrate-up

# 回滚迁移
make migrate-down
```

## 🛑 停止服务

```bash
# 停止 Go 应用
Ctrl+C

# 停止 Docker 服务
docker-compose -f deployments/docker-compose-minimal.yml down

# 停止并删除数据卷（⚠️ 会删除所有数据）
docker-compose -f deployments/docker-compose-minimal.yml down -v
```

## 🐛 故障排查

### 端口被占用

```bash
# 检查端口占用
lsof -i :8080   # Go 应用
lsof -i :3307   # MySQL
lsof -i :6379   # Redis
lsof -i :5672   # RabbitMQ
```

### Docker 服务无法启动

```bash
# 查看详细日志
docker-compose -f deployments/docker-compose-minimal.yml logs

# 重新创建容器
docker-compose -f deployments/docker-compose-minimal.yml up -d --force-recreate
```

### 环境变量未生效

```bash
# 检查 .env 文件
cat .env

# 验证环境变量
source .env
echo $MYSQL_PASSWORD

# 确保 Docker Compose 使用了 --env-file
docker-compose -f deployments/docker-compose-minimal.yml --env-file .env config
```

## 📚 下一步

- [环境变量配置详解](docs/ENV_CONFIG.md)
- [Agent 开发指南](docs/AGENT_DEVELOPMENT.md)（待创建）
- [API 文档](docs/API.md)（待创建）

## 🆘 获取帮助

- 查看文档：`docs/` 目录
- 提交 Issue：GitHub Issues
- 查看示例：`examples/` 目录

---

**🎉 恭喜！你已经成功启动了 AI Agent Arrange！**