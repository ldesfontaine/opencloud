#!/bin/bash
# Test du paquet sur une machine jetable avec systemd — le runner de la CI ou
# un conteneur —, jamais sur un poste de travail : installation, mise à jour,
# apt remove, réinstallation, apt purge. Sort 0 si tout tient, 1 sinon.
# usage : sudo packaging/test-install.sh <ancien.deb> <nouveau.deb>
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive

OLD_DEB=$(readlink -f -- "$1")
NEW_DEB=$(readlink -f -- "$2")
OLD_VERSION=$(dpkg-deb -f "$OLD_DEB" Version)
NEW_VERSION=$(dpkg-deb -f "$NEW_DEB" Version)
HEALTH_URL=http://127.0.0.1:8080/healthz
SRV_MARKER=/srv/workspace/prod/demo/marker

step() { printf '\n== %s\n' "$*"; }

fail() {
    printf 'ÉCHEC : %s\n' "$*" >&2
    journalctl -u opencloud --no-pager 2>/dev/null | tail -n 30 >&2 || true
    exit 1
}

# L'unité répond-elle avec la version attendue ? Trente secondes au plus.
wait_for_version() {
    local expected=$1
    for _ in $(seq 1 30); do
        if curl -fsS "$HEALTH_URL" 2>/dev/null | grep -q "\"version\":\"$expected\""; then
            return 0
        fi
        sleep 1
    done
    fail "l'interface ne répond pas en version $expected"
}

expect_owner_mode() {
    local path=$1 expected=$2 actual
    actual=$(stat -c '%U:%G %a' "$path")
    [ "$actual" = "$expected" ] || fail "$path : $actual, attendu $expected"
}

expect_active() {
    systemctl is-active --quiet opencloud.service || fail "l'unité n'est pas active"
    systemctl is-enabled --quiet opencloud.service || fail "l'unité n'est pas activée au démarrage"
}

step "témoin dans /srv, qu'aucun geste ne doit toucher"
mkdir -p "$(dirname "$SRV_MARKER")"
echo "intouché" > "$SRV_MARKER"

step "installation de $OLD_VERSION"
apt-get install -y "$OLD_DEB"
expect_active
wait_for_version "$OLD_VERSION"
expect_owner_mode /var/lib/opencloud "opencloud:opencloud 700"
expect_owner_mode /etc/opencloud/config.toml "root:opencloud 640"
expect_owner_mode /opt/opencloud/bin/opencloud "root:root 755"
[ -L /usr/bin/opencloud ] || fail "/usr/bin/opencloud n'est pas un lien vers le binaire"
[ -f /var/lib/opencloud/opencloud.db ] || fail "la base n'a pas été créée au premier démarrage"
expect_owner_mode /var/lib/opencloud/opencloud.db "opencloud:opencloud 600"
curl -fsS http://127.0.0.1:8080/login | grep -q "Connexion" || fail "la page de connexion ne répond pas"
opencloud status | grep -q "répond, version $OLD_VERSION" || fail "opencloud status ne voit pas le service"
[ "$(opencloud version)" = "opencloud $OLD_VERSION" ] || fail "opencloud version : $(opencloud version)"

step "l'opérateur touche sa configuration et son état"
echo "# note de l'opérateur" >> /etc/opencloud/config.toml
install -o opencloud -g opencloud -m 0600 /dev/null /var/lib/opencloud/key-marker

step "mise à jour vers $NEW_VERSION"
apt-get install -y "$NEW_DEB"
expect_active
wait_for_version "$NEW_VERSION"
grep -q "note de l'opérateur" /etc/opencloud/config.toml || fail "la configuration a été écrasée"
[ -f /var/lib/opencloud/key-marker ] || fail "l'état a été perdu à la mise à jour"
[ -f /var/lib/opencloud/opencloud.db ] || fail "la base a été perdue à la mise à jour"
[ "$(dpkg-query -W -f '${Version}' opencloud)" = "$NEW_VERSION" ] || fail "dpkg ne voit pas la nouvelle version"

step "apt remove : le binaire et l'unité partent, l'état reste"
apt-get remove -y opencloud
systemctl is-active --quiet opencloud.service && fail "l'unité tourne encore"
[ ! -e /usr/lib/systemd/system/opencloud.service ] || fail "l'unité est encore posée"
[ ! -e /opt/opencloud/bin/opencloud ] || fail "le binaire est encore posé"
[ ! -e /usr/bin/opencloud ] || fail "le lien /usr/bin/opencloud est encore posé"
[ -f /etc/opencloud/config.toml ] || fail "apt remove a retiré la configuration"
[ -f /var/lib/opencloud/opencloud.db ] || fail "apt remove a retiré la base"
[ -f /var/lib/opencloud/key-marker ] || fail "apt remove a retiré l'état"
getent passwd opencloud >/dev/null || fail "apt remove a retiré l'utilisateur, l'état en devient orphelin"

step "réinstallation : tout est retrouvé"
apt-get install -y "$NEW_DEB"
expect_active
wait_for_version "$NEW_VERSION"
grep -q "note de l'opérateur" /etc/opencloud/config.toml || fail "la configuration n'a pas été retrouvée"
[ -f /var/lib/opencloud/key-marker ] || fail "l'état n'a pas été retrouvé"

step "apt purge : plus rien d'openCloud"
apt-get purge -y opencloud
[ ! -e /etc/opencloud ] || fail "apt purge a laissé /etc/opencloud"
[ ! -e /var/lib/opencloud ] || fail "apt purge a laissé /var/lib/opencloud"
[ ! -e /opt/opencloud ] || fail "apt purge a laissé /opt/opencloud"
getent passwd opencloud >/dev/null && fail "apt purge a laissé l'utilisateur"

step "/srv n'a pas bougé"
[ "$(cat "$SRV_MARKER")" = "intouché" ] || fail "/srv a été touché"

printf '\nTout tient : installation, mise à jour, remove, réinstallation, purge.\n'
