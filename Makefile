# Cibles de développement. La CI joue les mêmes (.github/workflows/ci.yml).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

.PHONY: build test vet fmt lint vuln sec shellcheck plumber ci clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/opencloud ./cmd/opencloud

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint:
	go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

sec:
	go run github.com/securego/gosec/v2/cmd/gosec@latest ./...

shellcheck:
	@files=$$(find internal/scripts -name '*.sh' 2>/dev/null); \
	if [ -n "$$files" ]; then shellcheck $$files; else echo "aucun script"; fi

# Télécharge le binaire Plumber épinglé, vérifie son empreinte (et son
# attestation de provenance si gh le sait), puis joue la politique
# .plumber.yaml sur le workflow — comme la CI.
PLUMBER_VERSION = v0.4.52
PLUMBER_SHA256  = fb0943f49634da7678456ab428fb87e884c23af0b457d8ce9854eaa3f27700f7
PLUMBER_BIN     = bin/plumber-$(PLUMBER_VERSION)

$(PLUMBER_BIN):
	mkdir -p bin
	gh release download $(PLUMBER_VERSION) --repo getplumber/plumber --pattern plumber-linux-amd64 --output $@ --clobber
	echo "$(PLUMBER_SHA256)  $@" | sha256sum -c -
	gh attestation verify $@ --repo getplumber/plumber 2>/dev/null || echo "attestation non vérifiée : gh trop ancien, empreinte seule"
	chmod +x $@

plumber: $(PLUMBER_BIN)
	GITHUB_TOKEN=$$(gh auth token) $(PLUMBER_BIN) analyze --config .plumber.yaml --min-points 100 --fail-warnings

ci: vet lint test vuln sec shellcheck plumber build

clean:
	rm -rf bin/
