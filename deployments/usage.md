📦 deployments 目录结构

deployments/
├── docker/                    # Docker 相关配置
│   └── Dockerfile            # 构建 Docker 镜像的配置
├── k8s/                      # Kubernetes (生产环境) 配置
│   ├── deployment.yaml       # Pod 部署配置
│   ├── service.yaml         # 服务暴露配置
│   └── configmap.yaml       # 配置文件映射
└── docker-compose.yml        # 本地开发环境一键启动

🎯 三种部署场景

1. 本地开发 - 使用 docker-compose.yml

# 一键启动所有依赖服务（Redis、MySQL、RabbitMQ、Milvus 等）
docker-compose -f deployments/docker-compose.yml up -d

# 你的应用代码直接在本地运行
go run cmd/server/main.go

适用场景：
- 日常开发调试
- 快速验证功能
- 不需要 K8s 的复杂性

---
2. 容器化部署 - 使用 docker/Dockerfile

# 构建 Docker 镜像
docker build -f deployments/docker/Dockerfile -t ai-agent-arrange:latest .

# 运行容器
docker run -p 8080:8080 ai-agent-arrange:latest

适用场景：
- 打包应用为独立容器
- 便于分发和部署
- 可以推送到镜像仓库（Docker Hub、阿里云等）

---
3. 生产环境 - 使用 k8s/

# 部署到 Kubernetes 集群
kubectl apply -f deployments/k8s/

# 实现自动扩缩容、负载均衡、高可用

适用场景：
- 生产环境部署
- 需要高可用、自动扩容
- 大规模集群管理

---
🔄 典型工作流

本地开发 (docker-compose)
↓
容器化测试 (Dockerfile)
↓
生产部署 (Kubernetes)

💡 你现在需要做什么？

如果你只是想快速开始开发，只需要：

# 1. 启动依赖服务
cd /Users/wepie/Desktop/AI\ Agent/AI-Agent-Arrange
docker-compose -f deployments/docker-compose.yml up -d

# 2. 等待服务启动（约 30 秒）
docker-compose -f deployments/docker-compose.yml ps

# 3. 在另一个终端运行你的应用
go run cmd/server/main.go

这样你就有了：
- ✅ Redis (6379)
- ✅ MySQL (3306)
- ✅ RabbitMQ (5672, 管理界面 15672)
- ✅ Prometheus + Grafana (监控)
- ✅ Milvus (向量数据库)

Kubernetes 配置暂时不用关心，那是后期生产环境才需要的。