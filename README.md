# Antimoney

A modern web-based double-entry accounting application.

## Prerequisites
- Docker & Docker Compose
- Go 1.24+
- Node.js & npm

## Development

Use the provided Makefile for common tasks:

- `make build` - Build the backend and front-end locally.
- `make up` - Start the Docker containers (PostgreSQL, backend server, and frontend server).
- `make down` - Stop the Docker containers.

## Architecture

- **Backend**: Go with Chi router and pgx for fast, concurrent requests.
- **Frontend**: React, TypeScript, and Vite.
- **Database**: PostgreSQL with optimistic concurrency control.
