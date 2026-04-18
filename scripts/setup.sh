#!/bin/bash
# L.S.D Quick Setup & Run Script

set -e

echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           L.S.D Quick Setup & Run                       ║"
echo "╚═══════════════════════════════════════════════════════════╝"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

check_command() {
    if command -v "$1" &> /dev/null; then
        echo -e "${GREEN}✓${NC} $1 found"
        return 0
    else
        echo -e "${RED}✗${NC} $1 NOT found"
        return 1
    fi
}

echo -e "${YELLOW}Checking prerequisites...${NC}"
check_command go
check_command docker
check_command docker-compose

echo ""
echo -e "${YELLOW}Starting services with Docker Compose...${NC}"

# Start all services
docker-compose up -d postgres redis clickhouse

echo "Waiting for services to be ready..."
sleep 10

# Check PostgreSQL
for i in {1..30}; do
    if docker exec lsd-postgres pg_isready -U lsd &> /dev/null; then
        echo -e "${GREEN}✓ PostgreSQL ready${NC}"
        break
    fi
    sleep 1
done

# Check Redis
for i in {1..10}; do
    if docker exec lsd-redis redis-cli ping &> /dev/null; then
        echo -e "${GREEN}✓ Redis ready${NC}"
        break
    fi
    sleep 1
done

# Check ClickHouse
for i in {1..10}; do
    if docker exec lsd-clickhouse wget -q -O /dev/null http://localhost:8123/ping &> /dev/null; then
        echo -e "${GREEN}✓ ClickHouse ready${NC}"
        break
    fi
    sleep 1
done

echo ""
echo -e "${YELLOW}Setting up databases...${NC}"

# Run the database setup script
chmod +x scripts/generate_databases.sh
./scripts/generate_databases.sh

echo ""
echo -e "${YELLOW}Building API...${NC}"
docker-compose build api

echo ""
echo -e "${YELLOW}Starting API server...${NC}"
docker-compose up -d api

echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║  L.S.D is now running!                              ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""
echo "  API:         http://localhost:8080"
echo "  Health:      http://localhost:8080/api/health"
echo "  Dashboard:   http://localhost:8080/dashboard"
echo "  Swagger:    http://localhost:8080/docs"
echo "  PGAdmin:     http://localhost:5050"
echo "  Adminer:     http://localhost:8081"
echo ""
echo "  Default credentials:"
echo "    PGAdmin: admin@lsd.local / admin"
echo "    Adminer: admin / admin"
echo ""
echo "Run 'docker-compose logs -f' to see logs"
echo "Run 'docker-compose down' to stop all services"