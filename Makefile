.PHONY: setup backend frontend build dev serve worker test check smoke

setup:
	go mod download
	pnpm --dir web install --frozen-lockfile

backend:
	node scripts/build.mjs

frontend:
	pnpm --dir web build

build: backend frontend

dev: backend
	node scripts/dev.mjs

serve: backend
	./bin/blux serve

worker: backend
	./bin/blux worker

test:
	go test -race ./...

check:
	go vet ./...
	pnpm --dir web build

smoke: backend
	python3 scripts/smoke.py
