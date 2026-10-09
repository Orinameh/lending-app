.PHONY: help build vet run stop migrate-up migrate-down migrate-install test clean key

GOOSE_VERSION := v3.28.0

help:
	@echo "build          - build API binary"
	@echo "vet            - go vet ./..."
	@echo "run            - docker compose up"
	@echo "stop           - docker compose down"
	@echo "migrate-up     - apply goose migrations"
	@echo "migrate-down   - rollback one migration"
	@echo "migrate-install- install pinned goose CLI ($(GOOSE_VERSION))"
	@echo "test           - go test ./..."
	@echo "clean          - remove volumes and binaries"
	@echo "key            - generate a new ENCRYPTION_KEY"

build:
	go build -o backend/bin/api ./backend/cmd/api

vet:
	go vet ./...

run:
	docker compose up -d --build

stop:
	docker compose down

migrate-up:
	cd backend && goose -dir migrations postgres "$$DATABASE_URL" up

migrate-down:
	cd backend && goose -dir migrations postgres "$$DATABASE_URL" down

migrate-install:
	go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

test:
	cd backend && go test ./... -v -cover

clean:
	docker compose down -v
	cd backend && rm -rf bin/

key:
	@openssl rand -base64 32