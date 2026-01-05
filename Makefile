.PHONY: lint vet test

lint: vet
	golangci-lint run

vet:
	go vet ./...

test:
	go test -v ./...
