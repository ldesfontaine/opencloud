# openCloud — build, run, test, ci. Les outils sont épinglés par version ici
# et nulle part ailleurs ; la CI appelle ces mêmes cibles.

MODULE      := github.com/ldesfontaine/opencloud
VERSION     ?= v0.0.1
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS     := -s -w -X $(MODULE)/internal/version.number=$(VERSION) -X $(MODULE)/internal/version.commit=$(COMMIT)

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOIMPORTS   := golang.org/x/tools/cmd/goimports@v0.50.0
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.8.0
GOSEC       := github.com/securego/gosec/v2/cmd/gosec@v2.29.0

export CGO_ENABLED = 0

.PHONY: build run test ci fmt vet staticcheck vulncheck gosec clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/opencloud ./cmd/opencloud

# dev/ est ton /etc et ton /var/lib locaux : créé une fois, jamais effacé.
run: build
	@mkdir -p dev/state
	@test -f dev/config.toml || printf 'listen = "127.0.0.1:8080"\nstate_dir = "%s/dev/state"\n' "$(CURDIR)" > dev/config.toml
	./bin/opencloud serve -config dev/config.toml

test:
	go test ./...

ci: fmt vet staticcheck test vulncheck gosec build

fmt:
	@unformatted="$$(gofmt -l . ; go run $(GOIMPORTS) -l .)"; \
	if [ -n "$$unformatted" ]; then echo "à formater :"; echo "$$unformatted"; exit 1; fi

vet:
	go vet ./...

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

gosec:
	go run $(GOSEC) -quiet ./...

clean:
	rm -rf bin/opencloud dist
