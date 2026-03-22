.PHONY: up down build run logs test-frontend test-backend test e2e convert-csv

up:
	docker compose up -d

down:
	docker compose down

build:
	docker compose build

run:
	docker compose up

logs:
	docker compose logs -f

test-frontend:
	cd frontend && npm run test

test-backend:
	cd backend && go test -cover ./...

test: test-backend test-frontend

e2e:
	cd frontend && npx playwright test

convert-csv:
	cd scripts && node convert_csv.js
