include ./.env
export

run:
	reflex -r '\.go' -s -- sh -c 'go run ./main.go'

mig-up:
	goose -s -dir ./sql up

mig-down:
	goose -s -dir ./sql down

test:
	go test ./...

lint:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:v2.0.2 golangci-lint run

migrate-config:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:v2.0.2 golangci-lint migrate