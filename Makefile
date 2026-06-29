.PHONY: run build test vet tidy fmt redis

run:
	go run ./cmd/gateway -config config.yaml

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

fmt:
	gofmt -l -w .

redis:
	docker compose up -d redis
