#!/bin/bash
# Test de `opencloud self-update` en vrai, joué par le workflow release après
# la publication : installe la release précédente, se met à jour vers celle
# qui vient de sortir, vérifie que l'interface répond en nouvelle version.
# usage : sudo -E packaging/test-self-update.sh <tag précédent> <tag nouveau>
# Attend GH_TOKEN dans l'environnement : téléchargements et attestations d'un
# dépôt privé.
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive

PREVIOUS_TAG=$1
NEW_TAG=$2
REPOSITORY=ldesfontaine/opencloud
WORK_DIR=$(mktemp -d)

fail() {
    printf 'ÉCHEC : %s\n' "$*" >&2
    journalctl -u opencloud --no-pager 2>/dev/null | tail -n 30 >&2 || true
    exit 1
}

wait_for_version() {
    local expected=$1
    for _ in $(seq 1 30); do
        if curl -fsS http://127.0.0.1:8080/healthz 2>/dev/null | grep -q "\"version\":\"$expected\""; then
            return 0
        fi
        sleep 1
    done
    fail "l'interface ne répond pas en version $expected"
}

printf '\n== installation de la release précédente %s\n' "$PREVIOUS_TAG"
gh release download "$PREVIOUS_TAG" --repo "$REPOSITORY" --pattern '*.deb' --dir "$WORK_DIR"
apt-get install -y "$WORK_DIR"/opencloud_*_amd64.deb
wait_for_version "${PREVIOUS_TAG#v}"

printf '\n== jeton de lecture dans la configuration (dépôt privé)\n'
# La clé peut manquer si la release précédente est antérieure à son ajout. Un
# jeton GitHub ne contient que lettres, chiffres et soulignés : sûr pour sed.
grep -q '^github_token' /etc/opencloud/config.toml || echo 'github_token = ""' >> /etc/opencloud/config.toml
sed -i "s|^github_token = .*|github_token = \"$GH_TOKEN\"|" /etc/opencloud/config.toml
grep -q "^github_token = \"$GH_TOKEN\"" /etc/opencloud/config.toml || fail "le jeton n'a pas été écrit dans la configuration"

printf '\n== opencloud self-update --check\n'
opencloud self-update --check

printf '\n== opencloud self-update --version %s\n' "$NEW_TAG"
opencloud self-update --version "$NEW_TAG"
wait_for_version "${NEW_TAG#v}"
[ -f /opt/opencloud/bin/opencloud.prev ] || fail "l'ancien binaire n'a pas été gardé en .prev"
[ "$(/opt/opencloud/bin/opencloud.prev version)" = "opencloud ${PREVIOUS_TAG#v}" ] || fail ".prev n'est pas l'ancienne version"
[ "$(opencloud version)" = "opencloud ${NEW_TAG#v}" ] || fail "le binaire en place n'est pas la nouvelle version"

printf '\n== une seconde fois : rien à faire\n'
opencloud self-update | grep -q "déjà" || fail "la seconde exécution devait dire qu'il n'y a rien à faire"

printf '\nLa mise à jour en place tient : %s → %s.\n' "$PREVIOUS_TAG" "$NEW_TAG"
