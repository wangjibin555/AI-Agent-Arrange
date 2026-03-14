#!/bin/bash

# AI Agent Arrange - 开发环境启动脚本

set -e

echo "🚀 Starting AI Agent Arrange Development Environment"
echo "=================================================="

# 检查 .env 文件
if [ ! -f ".env" ]; then
    echo "⚠️  .env file not found!"
    echo "📝 Creating .env from .env.example..."
    cp .env.example .env
    echo "✅ Please edit .env file and set your API keys and passwords"
    exit 1
fi

# 启动 Docker 服务
echo "🐳 Starting Docker services..."
docker-compose -f deployments/docker-compose-minimal.yml --env-file .env up -d

# 等待服务就绪
echo "⏳ Waiting for services to be ready..."
sleep 10

# 检查服务状态
echo "📊 Checking service status..."
docker-compose -f deployments/docker-compose-minimal.yml --env-file .env ps

# 测试连接
echo ""
echo "🧪 Testing service connections..."
docker exec ai-agent-redis redis-cli ping > /dev/null && echo "✅ Redis: OK" || echo "❌ Redis: FAILED"
docker exec ai-agent-mysql mysql -uroot -p${MYSQL_PASSWORD} -e "SELECT 1" > /dev/null 2>&1 && echo "✅ MySQL: OK" || echo "❌ MySQL: FAILED"

echo ""
echo "=================================================="
echo "✅ Development environment is ready!"
echo ""
echo "📝 Service Access:"
echo "   RabbitMQ UI: http://localhost:15672 (guest/guest)"
echo ""
echo "🚀 To start the Go application:"
echo "   go run cmd/server/main.go"
echo ""
echo "🛑 To stop services:"
echo "   docker-compose -f deployments/docker-compose-minimal.yml down"
echo "=================================================="