.PHONY: itest
itest:
	go test ./tests -v -count=1

.PHONY: utest
utest:
	go test $(go list ./... | grep -v "/tests") -v -race -count=1 -short

.PHONY: lint
lint:
	golangci-lint run --verbose
