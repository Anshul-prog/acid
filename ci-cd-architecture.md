# =============================================================================
# ACID — CI/CD Architecture
# =============================================================================

## Overview

ACID uses a straightforward CI/CD pipeline designed around the Docker-based
infrastructure. The pipeline validates code quality, builds the binary, tests
connectivity, and promotes to production.

```
Developer Push
      │
      ▼
  ┌─────────────┐     ┌─────────────────┐     ┌──────────────┐
  │  Git Push   │────►│  CI: Build &    │────►│  Docker Push │
  │  (main/dev) │     │  Test           │     │  (GHCR/ECR)  │
  └─────────────┘     └─────────────────┘     └──────────────┘
                                                      │
                                                      ▼
                                              ┌──────────────┐
                                              │  Deploy to   │
                                              │  Staging/    │
                                              │  Production  │
                                              └──────────────┘
```

---

## CI Pipeline Stages

### Stage 1 — Code Quality
```yaml
- name: Lint
  run: |
    go vet ./...
    go install golang.org/x/lint/golint@latest
    golint ./...

- name: Format Check
  run: |
    if [ -n "$(gofmt -l .)" ]; then exit 1; fi
```

### Stage 2 — Build
```yaml
- name: Build Binary
  run: |
    go build -o build/acid-server ./cmd/api
    echo "Build size: $(du -sh build/acid-server)"

- name: Deploy Check
  run: |
    ./scripts/deploy-check.sh --env ci
```

### Stage 3 — Test
```yaml
- name: Unit Tests
  run: go test ./...

- name: Integration Tests (with Docker services)
  services:
    postgres:
      image: postgres:16-alpine
      env:
        POSTGRES_DB: acid_test
        POSTGRES_USER: acid
        POSTGRES_PASSWORD: test_pass
    redis:
      image: redis:7-alpine
  run: |
    export DATABASE_URL=postgres://acid:test_pass@localhost:5432/acid_test
    export REDIS_ADDR=localhost:6379
    go test ./... -tags integration
```

### Stage 4 — Docker Build
```yaml
- name: Build Docker Image
  run: |
    docker build -t acid-api:${{ github.sha }} .
    docker tag acid-api:${{ github.sha }} acid-api:latest

- name: Push to Registry
  run: |
    docker push ghcr.io/${{ github.repository }}/acid-api:${{ github.sha }}
    docker push ghcr.io/${{ github.repository }}/acid-api:latest
```

---

## Services Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                          Docker Compose Stack                        │
│                                                                      │
│  ┌────────────┐    ┌────────────┐    ┌──────────────────────────┐  │
│  │  ACID API  │    │  Postgres  │    │       ClickHouse         │  │
│  │  :8080     │───►│  :5432     │    │  :9000 (native)          │  │
│  │  (Go)      │    │            │    │  :8123 (HTTP)            │  │
│  └────────────┘    └────────────┘    └──────────────────────────┘  │
│        │                │                        ▲                   │
│        │           CDC Pipeline                  │                   │
│        │           (background goroutine)────────┘                  │
│        │                                                             │
│        │           ┌────────────┐                                   │
│        └──────────►│   Redis    │                                   │
│                    │   :6379    │                                   │
│                    │  (cache)   │                                   │
│                    └────────────┘                                   │
└─────────────────────────────────────────────────────────────────────┘
```

### Database Directory Structure

```
databases/
├── generator.go          # Go data generator (10 DBs × 1000 tables × 50 users)
├── categories.sql        # Categories & entity_categories schema + seed data
├── init-clickhouse.sql   # ClickHouse schema for full-text search index
├── migrations/           # SQL migration files (applied at startup)
├── seeds/                # Seed data SQL files (run manually)
├── incoming/             # DROP CSV/Excel files here → pipeline picks them up
│                         # POST /api/pipeline/start {"folder_path":"databases/incoming"}
├── archive/              # Pipeline moves processed files here on success
├── private_nosql/        # Static JSON files served at /api/private/nosql/*
│   └── hadoop_review.json  # Served at /api/private/nosql/hadoop-review
└── README.md             # Usage guide
```

---

## Pipeline Import Workflow

The ACID pipeline processor watches `databases/incoming/` for files.

| Step | Action |
|---|---|
| 1 | Drop `.csv` or `.xlsx` file into `databases/incoming/` |
| 2 | Call `POST /api/pipeline/start` with `{"folder_path": "databases/incoming"}` |
| 3 | Monitor progress: `GET /api/pipeline/jobs/{job_id}` |
| 4 | Stream live logs: `GET /api/pipeline/jobs/{job_id}/stream` |
| 5 | Processed files move to `databases/archive/` |
| 6 | Failed rows logged to `ErrorFiles/` |

---

## Environment Promotion

| Environment | Branch | Auto-Deploy | Services |
|---|---|---|---|
| Development | `feature/*` | No | Local DB + Redis |
| Staging | `develop` | Yes (CI) | Docker Compose |
| Production | `main` | Manual approval | Managed DB + Redis Cluster |

---

## Deployment Commands

### Docker Compose (Staging/Dev)
```bash
# Full stack (API + PG + Redis + ClickHouse)
docker compose up -d

# Just the API (connect to external DB)
docker compose up -d acid-api
```

### Manual Production (Linux)
```bash
# Build
go build -o /opt/acid/build/acid-server ./cmd/api

# Install systemd service
sudo cp scripts/acid-api.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable acid-api
sudo systemctl start acid-api

# Check status
sudo systemctl status acid-api
journalctl -u acid-api -f
```

### Validation Scripts

```bash
# Pre-deploy checks
./scripts/preflight.sh
./scripts/deploy-check.sh --env production

# DB validation
./scripts/db-validate.sh

# Stop server
./scripts/stop-backend.sh

# Analytics
python scripts/analytics.py --output reports/analytics.csv
```
