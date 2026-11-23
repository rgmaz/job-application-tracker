.PHONY: build test lint generate migrate run docker sqlc-check clean

BIN ?= ./jat

build: generate
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/server

test:
	go test -race ./...

lint:
	go vet ./...
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi

generate:
	sqlc generate

migrate:
	go run ./cmd/server migrate

run: build
	$(BIN) serve

docker:
	docker build -t jat:dev .

sqlc-check: generate
	@git diff --exit-code -- internal/store/db >/dev/null || ( \
		echo "sqlc drift: run 'make generate' and commit internal/store/db" >&2; \
		exit 1)

clean:
	rm -f $(BIN)
