.PHONY: lint build vet test ci

lint:
	golangci-lint run

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./... -count=1

ci: lint build vet test
