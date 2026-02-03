.PHONY: lint vet test mocks

lint: vet
	golangci-lint run

vet:
	go vet ./...

test:
	go test -v ./...

mocks:
	mockery --name=InstanceServer --structname=MockInstanceServer --srcpkg=github.com/lxc/incus/v6/client --output=. --outpkg=main --filename=mock_incus_test.go --inpackage=false
	mockery --name=Operation --structname=MockOperation --srcpkg=github.com/lxc/incus/v6/client --output=. --outpkg=main --filename=mock_operation_test.go --inpackage=false
