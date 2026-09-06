# Cibles de développement. La CI joue les mêmes (.github/workflows/ci.yml).
#
# VERSION : le tag sans son v (0.0.2) pour une release ; git describe sinon
# (0.0.1-3-gabc-dirty), que self-update refuse comme version de développement.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

# Build reproductible : -trimpath, pas de cgo, pas d'empreinte VCS — elle
# gravait l'état « modifié » de l'arbre de travail dans le binaire, deux
# builds du même commit différaient —, et la date du commit pour les
# horodatages du paquet (nfpm lit SOURCE_DATE_EPOCH).
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
export SOURCE_DATE_EPOCH

NFPM_VERSION = v2.47.0
DIST ?= dist

.PHONY: build run test vet fmt lint vuln sec shellcheck plumber ci release reproducible package-test clean

build:
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/opencloud ./cmd/opencloud

# Lance le binaire depuis le dépôt avec dev/config.toml (hors git) : rien
# n'est installé sur la machine, l'état vit dans dev/state.
run: build
	bin/opencloud serve --config dev/config.toml

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

sec:
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 ./...

shellcheck:
	@files="$$(find internal/scripts -name '*.sh' 2>/dev/null) packaging/postinst packaging/prerm packaging/postrm packaging/*.sh"; \
	shellcheck $$files

# Une release : le binaire nu, le paquet .deb et leurs sommes, dans dist/.
# Le workflow release joue la même cible sur un tag, puis atteste et publie.
release: build
	rm -rf $(DIST) && mkdir -p $(DIST)
	cp bin/opencloud $(DIST)/opencloud_$(VERSION)_linux_amd64
	VERSION=$(VERSION) go run github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION) package --config packaging/nfpm.yaml --packager deb --target $(DIST)/
	cd $(DIST) && sha256sum opencloud_$(VERSION)_linux_amd64 opencloud_$(VERSION)_amd64.deb > SHA256SUMS

# Deux builds du même commit, cache Go vidé entre les deux : mêmes sommes.
reproducible:
	$(MAKE) release DIST=$(DIST)/first
	go clean -cache
	$(MAKE) release DIST=$(DIST)/second
	cd $(DIST)/first && sha256sum -c ../second/SHA256SUMS

# Le test du paquet, dans un conteneur Debian avec systemd — jamais sur le
# poste de travail. Construit deux versions et joue packaging/test-install.sh.
package-test:
	$(MAKE) release VERSION=0.0.1 DIST=$(DIST)/test-old
	$(MAKE) release VERSION=0.0.2 DIST=$(DIST)/test-new
	docker build -q -t opencloud-package-test packaging/test-image >/dev/null
	docker rm -f opencloud-package-test >/dev/null 2>&1 || true
	docker run -d --name opencloud-package-test --privileged --cgroupns=host \
		-v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		-v "$(CURDIR)/packaging:/packaging:ro" -v "$(CURDIR)/$(DIST):/dist:ro" \
		opencloud-package-test >/dev/null
	docker exec opencloud-package-test /packaging/test-install.sh /dist/test-old/opencloud_0.0.1_amd64.deb /dist/test-new/opencloud_0.0.2_amd64.deb; \
	status=$$?; docker rm -f opencloud-package-test >/dev/null; exit $$status

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
	rm -rf bin/ dist/
