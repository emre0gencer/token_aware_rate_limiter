.PHONY: run stub build test vet tidy fmt redis compose compose-down invariant invariant-split

run:
	go run ./cmd/gateway -config config.yaml

# Run the fake LLM upstream locally (for `make run` end-to-end without a provider).
stub:
	go run ./cmd/stubllm

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

# --- step-6 multi-node invariant ------------------------------------------------

# Shared-Redis stack: redis + stubllm + gateway1(:8081) + gateway2(:8082).
compose:
	docker compose up --build -d

compose-down:
	docker compose -f docker-compose.yml -f docker-compose.split.yml down -v

# Prove the invariant holds with two replicas on one shared Redis.
invariant:
	./scripts/invariant.sh

# The negative control: gateway2 gets its own Redis => admitted spend doubles.
invariant-split:
	docker compose -f docker-compose.yml -f docker-compose.split.yml up --build -d
	./scripts/invariant.sh
