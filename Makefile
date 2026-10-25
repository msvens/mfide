.PHONY: lint build vet test ci release

lint:
	golangci-lint run

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./... -count=1

ci: lint build vet test

# make release VERSION=x.y.z [DRY_RUN=1]: see scripts/release.sh
release:
	VERSION=$(VERSION) DRY_RUN=$(DRY_RUN) scripts/release.sh
