.PHONY: build test vet fmt extension install
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/desk-agent .
test:
	go test -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -l -w .
extension:
	extension/build.sh
install:
	./install.sh
