# Antimoney 💰

Antimoney is a modern, high-performance web-based accounting application designed for individuals and small businesses who value privacy, speed, and a premium user experience. Built on the principles of **double-entry bookkeeping**, it ensures your financial data is always accurate and balanced.

<!-- ![Antimoney Dashboard](/Users/sander/.gemini/antigravity/brain/93389677-d6b1-4bee-9737-66dfac8baa51/antimoney_dashboard_preview_1773882236960.png) -->

---

## 🌟 For Users: Professional Personal Finance

Antimoney transforms the way you view your finances. No more spreadsheets or clunky legacy software.

### Key Features

- **Intuitive Dashboard**: At-a-glance view of your Net Worth, Assets, Liabilities, and Cash Flow. Visualize your spending with elegant donut charts and horizontal bars.
- **Double-Entry Bookkeeping**: A robust system that tracks every cent. Every transaction is a transfer between two or more accounts, ensuring total consistency.
- **Flexible Chart of Accounts**: Organise your finances into a hierarchical tree of Assets, Liabilities, Income, Expenses, and Equity.
- **Navigation Breadcrumbs**: Easily navigate large account hierarchies with a dynamic breadcrumb bar at the top of every account register.
- **Persistent State**: The application remembers which accounts you've expanded or collapsed in the Chart of Accounts, preserving your workspace between sessions.
- **Reconciliation Wizard**: Easily reconcile your bank statements. The system suggests balances based on specific days and highlights differences in real-time.
- **Advanced Filtering**: Switch between "Today's Balance" and "Overall Balance" in the Chart of Accounts with a single click.
- **Interactive Register**:
  - **Quick Deletion**: Delete transactions directly from any account register with a single click.
  - **Jump Navigation**: Instantly jump from one side of a transaction to its counterpart account.
- **Searchable Account Picker**: Efficiently find accounts when creating transactions with a powerful, searchable combobox.
- **Multi-Split Transactions**: One transaction can be split across multiple accounts (e.g., a single grocery bill split into food and household items).
- **Import/Export**: Move your data freely with CSV and JSON import/export support.
- **Premium Dark UI**: A sleek, dark-mode interface with glassmorphism effects and high-contrast highlights for easy navigation.
- **Bi-lingual Support**: Full support for English and Portuguese (pt-BR).

### How to use it?

1.  **Set up your accounts**: Create your bank accounts, credit cards, and income/expense categories in the **Chart of Accounts**.
2.  **Record transactions**: Use the **New Transaction** button (Shortcut: `N`) to record your daily spending.
3.  **Reconcile regularly**: Use the **Reconciliation** button in any account to ensure your digital balance matches your physical bank statement.
4.  **Analyze**: Use the **Dashboard** to track your cash flow and identify where your money is going.

---

## 🛠 For Developers: Modern Stack & Architecture

Antimoney is designed for performance, scalability, and ease of deployment. It uses a split-architecture approach with a Go backend and a React frontend.

### Technology Stack

- **Backend**: 
  - **Language**: Go 1.24+
  - **Web Framework**: Chi Router (minimal, fast, and idiomatic)
  - **Database Driver**: `pgx` for high-performance PostgreSQL interaction.
  - **Migrations**: Automated database schema management.
- **Frontend**:
  - **Framework**: React 18+ with TypeScript.
  - **Build Tool**: Vite (blazing fast development and bundling).
  - **State Management**: React Hooks & TanStack Query (for data fetching/caching).
  - **Styling**: Vanilla CSS with a customized design system (CSS variables, glassmorphism).
- **Infrastructure**:
  - **Containerization**: Docker & Docker Compose.
  - **Cloud Infrastructure**: Terraform for Google Cloud Platform (Cloud Run, Cloud SQL, Artifact Registry).
  - **Automated Cleanup**:
    - **Source Artifacts**: GCS staging bucket has a 7-day TTL lifecycle policy.
    - **Container Images**: Artifact Registry policy automatically keeps the last 5 tagged images and removes untagged ones.
  - **Security**: Database sits in private network; JWT-based authentication.

### Getting Started

#### Prerequisites
- Docker & Docker Compose
- Go 1.24+
- Node.js 18+ & npm

#### Running Locally
The easiest way to start developing is using the provided `Makefile`:

```bash
# Build both frontend and backend
make build

# Start the full stack (Postgres, Backend, Frontend)
make up

# Stop the containers
make down
```

The application will be available at `http://localhost:5173` (Frontend) and `http://localhost:8080/api` (Backend).

### Deployment

Antimoney includes a full CI/CD pipeline for Google Cloud Platform.

1.  **Initialize Infrastructure**:
    ```bash
    cd infra
    terraform init
    terraform apply
    ```
2.  **Deploy to Cloud Run**:
    ```bash
    ./deploy.sh
    ```
    This script builds the Docker images via Cloud Build, pushes them to Artifact Registry, and deploys the service to Cloud Run.

### Project Structure

- `/cmd/server`: Entry point for the Go API server.
- `/internal`: Core business logic, database handlers, and accounting engine.
- `/frontend/src`: React application sources.
- `/infra`: Terraform configuration for GCP.
- `/migrations`: SQL files for database versioning.

---

## ⚖ License

This project is open-source. See the LICENSE file for more details.
