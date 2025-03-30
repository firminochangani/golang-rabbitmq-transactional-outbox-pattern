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