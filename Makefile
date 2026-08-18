.PHONY: test vet e2e e2e-loop

test:
	go test ./...

vet:
	go vet ./...

e2e:
	go test ./internal/benchmark -run PublicationE2E -count=1

e2e-loop:
	go test ./internal/benchmark -run PublicationE2E -count=3
