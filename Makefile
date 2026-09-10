# Cibles de développement. La CI GitHub joue les mêmes, et davantage (cible `ci`).
# bin/ : le binaire de dev et les outils. dist/ : ce qu'une release publie.
# dev/ : ta configuration et ton état locaux, à toi, jamais régénérés.
#
# VERSION : le tag sans son v (0.0.2) pour une release ; git describe sinon
# (0.0.1-3-gabc-dirty), que self-update refuse comme version de développement.
VERSION ?= $(shell described=$$(git describe --tags --always --dirty 2>/dev/null); echo "$${described:-dev}" | sed 's/^v//')
LDFLAGS  = -s -w -X main.version=$(VERSION)

# Build reproductible : -trimpath, pas de cgo, pas d'empreinte VCS — elle
# gravait l'état « modifié » de l'arbre de travail dans le binaire, deux
# builds du même commit différaient —, et la date du commit pour les
# horodatages du paquet (nfpm lit SOURCE_DATE_EPOCH).
SOURCE_DATE_EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
export SOURCE_DATE_EPOCH

NFPM_VERSION = v2.47.0
DIST ?= dist

.PHONY: build run test vet fmt fmtcheck lint vuln sec shellcheck plumber ci release reproducible package-test temoin-up temoin-down clean

# La cible est fixée : le paquet déclare amd64, les binaires doivent l'être aussi.
# Le lanceur n'a pas de version : il ne se met à jour qu'avec le paquet.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/opencloud ./cmd/opencloud
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags '-s -w' -o bin/oc-launch ./cmd/oc-launch

# Lance le binaire depuis le dépôt : rien n'est installé sur la machine,
# l'état vit dans dev/state, que le binaire crée lui-même.
run: build dev/config.toml
	bin/opencloud serve --config dev/config.toml

# La configuration de dev naît au premier `make run` avec les valeurs par
# défaut, puis elle t'appartient : Make ne la récrit jamais tant qu'elle existe.
# Hors git, jamais touchée par `clean`.
dev/config.toml:
	mkdir -p dev
	printf '%s\n' \
		'# Configuration de développement, hors git. state_dir est relatif à ce fichier.' \
		'listen = "127.0.0.1:8080"' \
		'state_dir = "state"' \
		'' \
		'# Garde admin / opencloud sans obliger le changement. Jamais en production.' \
		'allow_default_password = true' > $@

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

fmtcheck:
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then echo "$$unformatted"; exit 1; fi

lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

sec:
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 ./...

shellcheck:
	@files="$$(find internal/scripts -name '*.sh' 2>/dev/null) packaging/preinst packaging/postinst packaging/prerm packaging/postrm packaging/*.sh"; \
	shellcheck $$files

# Une release : le binaire nu, le paquet .deb et leurs sommes, dans dist/.
# Le workflow release joue la même cible sur un tag, puis atteste et publie.
release: build
	rm -rf $(DIST) && mkdir -p $(DIST)
	cp bin/opencloud $(DIST)/opencloud_$(VERSION)_linux_amd64
	VERSION=$(VERSION) go run github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION) package --config packaging/nfpm.yaml --packager deb --target $(DIST)/
	cd $(DIST) && sha256sum opencloud_$(VERSION)_linux_amd64 opencloud_$(VERSION)_amd64.deb > SHA256SUMS

# Deux builds du même commit, cache Go vidé entre les deux : mêmes sommes.
# Le premier, dans DIST, est celui qu'on publie ; le second ne sert qu'à comparer.
reproducible: release
	go clean -cache
	$(MAKE) release DIST=$(DIST)-check
	cd $(DIST) && sha256sum -c $(CURDIR)/$(DIST)-check/SHA256SUMS

# Le test du paquet, dans un conteneur Debian avec systemd — jamais sur le
# poste de travail. Construit deux versions, joue packaging/test-install.sh,
# puis packaging/test-action.sh : amorçage, Diagnostiquer par l'interface,
# Poser le socle, Installer le proxy, reprise. Le conteneur est jetable, donc
# --avec-socle : lui seul a le droit de se faire installer Docker et de prendre
# les ports 80 et 443.
#
# Deux volumes anonymes pour /var/lib/docker et /var/lib/containerd : le Docker
# posé dans le conteneur y empile ses images, et overlayfs ne se monte pas sur
# overlayfs. « rm -f -v » les emporte avec le conteneur.
package-test:
	$(MAKE) release VERSION=0.0.1 DIST=$(DIST)/test-old
	$(MAKE) release VERSION=0.0.2 DIST=$(DIST)/test-new
	docker build -q -t opencloud-package-test packaging/test-image >/dev/null
	docker rm -f -v opencloud-package-test >/dev/null 2>&1 || true
	docker run -d --name opencloud-package-test --privileged --cgroupns=host \
		-v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		-v /var/lib/docker -v /var/lib/containerd \
		-v "$(CURDIR)/packaging:/packaging:ro" -v "$(CURDIR)/$(DIST):/dist:ro" \
		opencloud-package-test >/dev/null
	docker exec opencloud-package-test /packaging/test-install.sh /dist/test-old/opencloud_0.0.1_amd64.deb /dist/test-new/opencloud_0.0.2_amd64.deb \
		&& docker exec opencloud-package-test /packaging/test-action.sh /dist/test-new/opencloud_0.0.2_amd64.deb --avec-socle; \
	status=$$?; docker rm -f -v opencloud-package-test >/dev/null; exit $$status

# Le témoin : une machine Debian jetable avec systemd et sshd, à enrôler depuis
# l'interface pour développer l'enrôlement à distance. Son sshd est publié sur
# 127.0.0.1:2222 ; la commande d'enrôlement se joue dedans par
# « docker exec -i opencloud-temoin bash -c '<commande>' ».
TEMOIN_PORT ?= 2222

temoin-up:
	docker build -q -t opencloud-package-test packaging/test-image >/dev/null
	docker rm -f opencloud-temoin >/dev/null 2>&1 || true
	docker run -d --name opencloud-temoin --privileged --cgroupns=host \
		-v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		-p 127.0.0.1:$(TEMOIN_PORT):22 opencloud-package-test >/dev/null
	@echo "témoin prêt : SSH sur 127.0.0.1:$(TEMOIN_PORT), commande à jouer par « docker exec -i opencloud-temoin bash -c '…' »"

temoin-down:
	docker rm -f opencloud-temoin >/dev/null 2>&1 || true

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

# Le tour de vérification en local, avant de pousser. La CI GitHub
# (.github/workflows/ci.yml) est la référence : elle joue en plus `reproducible`
# et `package-test`, trop lents pour le poste de travail.
ci: fmtcheck vet lint test vuln sec shellcheck plumber build

# Jette ce que le dépôt produit ; garde l'outil plumber (35 Mo) et dev/.
clean:
	rm -rf dist/ bin/opencloud bin/oc-launch
