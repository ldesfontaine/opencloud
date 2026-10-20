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

FRONT_DIR   := web

export CGO_ENABLED = 0

.PHONY: build release run test ci fmt vet staticcheck vulncheck gosec front front-check clean

# Le front compilé est embarqué par go:embed : il précède tout build ou test Go.
build: front
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/opencloud ./cmd/opencloud

# Les binaires publiés : amd64 et arm64, statiques, avec leurs sommes.
release: front
	rm -rf dist-release && mkdir -p dist-release
	GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist-release/opencloud-linux-amd64 ./cmd/opencloud
	GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist-release/opencloud-linux-arm64 ./cmd/opencloud
	cd dist-release && sha256sum opencloud-linux-* > SHA256SUMS

# npm ci ne rejoue que si le verrou a changé ; le premier verrou se produit
# avec npx npm@10 install si le npm de la machine (9.x) plante.
$(FRONT_DIR)/node_modules/.stamp: $(FRONT_DIR)/package-lock.json
	cd $(FRONT_DIR) && npm ci && touch node_modules/.stamp

front: $(FRONT_DIR)/node_modules/.stamp
	find $(FRONT_DIR)/dist -mindepth 1 -not -name .gitkeep -delete
	cd $(FRONT_DIR) && npm run build

# L'audit précède les tests : une dépendance de production vulnérable arrête
# tout. `audit=false` du .npmrc ne coupe que l'audit de npm ci ; l'appel
# explicite interroge le registre (il faut donc le réseau).
front-check: $(FRONT_DIR)/node_modules/.stamp
	cd $(FRONT_DIR) && npm audit --omit=dev --audit-level=high
	cd $(FRONT_DIR) && npm run check

# dev/ est ton /etc et ton /var/lib locaux : créé une fois, jamais effacé.
run: build
	@mkdir -p dev/state
	@test -f dev/config.toml || printf 'listen = "127.0.0.1:8080"\nstate_dir = "%s/dev/state"\n' "$(CURDIR)" > dev/config.toml
	./bin/opencloud serve -config dev/config.toml

test: front
	go test ./...

ci: fmt vet staticcheck front-check test vulncheck gosec build

fmt:
	@unformatted="$$(gofmt -l . ; go run $(GOIMPORTS) -l .)"; \
	if [ -n "$$unformatted" ]; then echo "à formater :"; echo "$$unformatted"; exit 1; fi

# vet et staticcheck compilent web/ : l'embed exige dist/ rempli.
vet: front
	go vet ./...

staticcheck: front
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

gosec:
	go run $(GOSEC) -quiet ./...

clean:
	rm -rf bin/opencloud dist dist-release
	find $(FRONT_DIR)/dist -mindepth 1 -not -name .gitkeep -delete
