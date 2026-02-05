.PHONY: lint vet test mocks

lint: vet
	golangci-lint run

vet:
	go vet ./...

test:
	go test -v -coverprofile=cover.out ./...
	go tool cover -func=cover.out

mocks:
	mockery --name=InstanceServer --structname=MockInstanceServer --srcpkg=github.com/lxc/incus/v6/client --output=./mocks --outpkg=mocks --filename=mock_incus.go --inpackage=false
	mockery --name=Operation --structname=MockOperation --srcpkg=github.com/lxc/incus/v6/client --output=./mocks --outpkg=mocks --filename=mock_operation.go --inpackage=false
