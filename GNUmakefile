default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	@if command -v golangci-lint >/dev/null; then golangci-lint run; else echo "golangci-lint not installed; running go vet"; go vet ./...; fi

fmt:
	gofmt -s -w -e .

vet:
	go vet ./...

# Regenerates docs/ from the schemas, examples/ and templates/.
generate docs:
	go generate ./...

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

# Acceptance tests create real objects; see CONTRIBUTING.md.
testacc:
	TF_ACC=1 go test -v -cover -timeout 30m ./...

.PHONY: default build install lint fmt vet generate docs test testacc
