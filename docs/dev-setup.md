# Antimoney — Development Environment Setup

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| [Go](https://golang.org/dl/) | ≥ 1.22 | Backend |
| [Node.js](https://nodejs.org/) | ≥ 20 | Frontend |
| [Docker](https://docs.docker.com/get-docker/) | ≥ 24 | PostgreSQL (and optional full-stack compose) |
| [Docker Compose](https://docs.docker.com/compose/) | ≥ 2.20 | Multi-service orchestration |

---

## Option 1: Docker Compose (Recommended)

The simplest way to run everything. From the project root:

```bash
docker compose up --build
```

This starts:
- **PostgreSQL 16** on `localhost:5432`
- **Go API server** on `localhost:8000` (auto-runs migrations + seeds)
- **Vite dev server** on `localhost:5173` (hot-reload)

Open [http://localhost:5173](http://localhost:5173) in your browser.

To stop: `docker compose down`

To reset the database: `docker compose down -v && docker compose up --build`

---

## Option 2: Manual Setup (for Development)

### 1. Start PostgreSQL

```bash
docker run -d --name antimoney-db \
  -e POSTGRES_DB=antimoney \
  -e POSTGRES_USER=antimoney \
  -e POSTGRES_PASSWORD=antimoney_dev \
  -p 5432:5432 \
  postgres:16-alpine
```

### 2. Start the Backend

```bash
cd backend

# Download dependencies
go mod tidy

# Run the server (auto-runs migrations + seeds the database)
DATABASE_URL="postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable" \
  go run ./cmd/server/
```

The API will be available at `http://localhost:8000`.

Health check: `curl http://localhost:8000/health`

### 3. Start the Frontend

```bash
cd frontend

# Install dependencies
npm install

# Start dev server (proxies /api to localhost:8000)
npm run dev
```

Open [http://localhost:5173](http://localhost:5173).

---

## Running Tests

### Go Unit Tests (Rational Number Engine)

```bash
cd backend
go test ./internal/gnc/ -v
```

### Full Backend Tests

```bash
cd backend
go test ./... -v
```

---

## Project Structure

```
antimoney/
├── backend/
│   ├── cmd/server/main.go         # Entry point: migrations, seed, HTTP server
│   ├── internal/
│   │   ├── config/                # Environment config
│   │   ├── database/              # pgx connection pool + migrations
│   │   ├── gnc/                   # Rational number engine (num/denom)
│   │   ├── models/                # Domain models
│   │   ├── handlers/              # HTTP handlers (chi router)
│   │   ├── services/              # Business logic
│   │   └── seed/                  # Default currencies + Chart of Accounts
│   └── migrations/                # SQL migration files
├── frontend/
│   ├── src/
│   │   ├── api/                   # API client
│   │   ├── components/            # Sidebar, AccountTree, Register, TransactionForm
│   │   ├── pages/                 # Dashboard, Accounts, AccountRegister, Transactions
│   │   └── types/                 # TypeScript interfaces
│   └── ...
├── docker-compose.yml
└── docs/
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable` | PostgreSQL connection string |
| `PORT` | `8000` | Backend HTTP port |
| `ENVIRONMENT` | `development` | `development` or `production` |

---

## API Quick Reference

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check |
| `GET` | `/api/books` | Get current book |
| `GET/POST` | `/api/commodities` | List/create currencies |
| `GET/POST` | `/api/accounts` | List (tree with balances) / create |
| `GET/PUT/DELETE` | `/api/accounts/{id}` | Account CRUD |
| `GET` | `/api/accounts/{id}/register` | Account register (ledger view) |
| `GET/POST` | `/api/transactions` | List / create |
| `GET/DELETE` | `/api/transactions/{id}` | Get / delete transaction |
