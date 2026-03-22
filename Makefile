.PHONY: test-frontend test-backend test e2e

test-frontend:
	cd frontend && npm run test

test-backend:
	cd backend && go test -cover ./...

test: test-backend test-frontend

e2e:
	cd frontend && npx playwright test
