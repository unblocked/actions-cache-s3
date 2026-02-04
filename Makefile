VERSION := $(shell cat version.txt)

fmt:
	go fmt ./...
.PHONY: fmt

vet:
	go vet ./...
.PHONY: vet

build-dist: fmt vet
	env GOOS=linux GOARCH=amd64 go build -o dist/linux ./src
	env GOOS=windows GOARCH=amd64 go build -o dist/windows ./src
	env GOOS=darwin GOARCH=amd64 go build -o dist/macos ./src
.PHONY: build-dist

tag:
	git tag --force -a v$(VERSION) -m "Version $(VERSION)"
	git push --force --tags
.PHONY: tag

run-local:
	./run.sh $(args)
.PHONY: run-local

update-dependencies:
	go get -u ./...
	go mod tidy
.PHONY: update-dependencies

# Run unit tests only (no Docker required)
test-unit:
	go test -v ./src -run "TestZip|TestGetReadableBytes|TestOptimalPartSize"
.PHONY: test-unit

# Run all tests including S3 integration (requires Docker)
test:
	./scripts/test.sh
.PHONY: test

# Start MinIO for manual testing
test-minio-up:
	docker compose -f docker-compose.test.yml up -d
	@echo "MinIO console: http://localhost:9001 (minioadmin/minioadmin)"
.PHONY: test-minio-up

# Stop MinIO
test-minio-down:
	docker compose -f docker-compose.test.yml down -v
.PHONY: test-minio-down

