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
BIN_PATH=/opt/opencloud/bin/opencloud
# Le .deb déballé, les journaux d'apt et les faux systemd. Dans /var/tmp, pas
# /tmp : le test démarre pendant que la machine finit de démarrer, et
# systemd-tmpfiles vide /tmp à ce moment-là. Pas de nettoyage : sur une machine
# jetable, ce qui reste après un échec est ce qu'on veut relire.
WORK_DIR=$(mktemp -d -p /var/tmp)

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

# Reproduit ce que fait self-update : le binaire est remplacé par un rename,
# sans que la base dpkg en sache rien.
replace_binary_as_self_update_would() {
    local source=$1
    cp -- "$source" "$BIN_PATH.new"
    chmod 0755 "$BIN_PATH.new"
    mv -- "$BIN_PATH.new" "$BIN_PATH"
    systemctl restart opencloud.service
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

step "un démarrage qui échoue toujours finit par arrêter l'unité"
# Un ExecStart voué à l'échec, dans /run pour ne toucher ni /etc ni le paquet :
# c'est la boucle sans fin qui remplissait /var/lib d'une copie de la base par
# tentative. StartLimitBurst doit y mettre un terme.
mkdir -p /run/systemd/system/opencloud.service.d
printf '%s\n' '[Service]' 'ExecStart=' 'ExecStart=/bin/false' \
    > /run/systemd/system/opencloud.service.d/echec.conf
systemctl daemon-reload
systemctl restart opencloud.service || true
# Trente secondes, soit six fois RestartSec : sans borne, systemd aurait relancé
# six fois, et six copies de la base seraient dans backups/.
sleep 30
declared_burst=$(systemctl show -p StartLimitBurst --value opencloud.service)
observed_restarts=$(systemctl show -p NRestarts --value opencloud.service)
[ "$observed_restarts" -le "$declared_burst" ] \
    || fail "$observed_restarts redémarrages en 30 s pour un StartLimitBurst de $declared_burst : rien ne borne la boucle"
[ "$(systemctl is-failed opencloud.service || true)" = failed ] \
    || fail "l'unité n'est pas en failed : l'opérateur ne verra rien dans systemctl status"

step "poser le paquet rend son budget de démarrages à une unité qui a renoncé"
# Le compteur survit à la mise à jour : sans reset-failed, le paquet qui corrige
# la panne ne repartirait pas. C'est postinst qui doit s'en charger.
rm -rf /run/systemd/system/opencloud.service.d
systemctl daemon-reload
apt-get install -y --reinstall "$NEW_DEB"
expect_active
wait_for_version "$NEW_VERSION"

step "rétrograder par le paquet est refusé, et le refus se lit"
if apt-get install -y --allow-downgrades "$OLD_DEB" > "$WORK_DIR/downgrade.log" 2>&1; then
    fail "la rétrogradation vers $OLD_VERSION est passée sans un mot"
fi
grep -q "refuse de rétrograder" "$WORK_DIR/downgrade.log" || fail "le refus ne se nomme pas : $(cat "$WORK_DIR/downgrade.log")"
grep -q "OPENCLOUD_ALLOW_DOWNGRADE" "$WORK_DIR/downgrade.log" || fail "le refus ne donne pas le moyen de forcer"
[ "$(opencloud version)" = "opencloud $NEW_VERSION" ] || fail "le binaire a reculé malgré le refus"
expect_active

step "rétrograder reste possible quand on le demande explicitement"
env OPENCLOUD_ALLOW_DOWNGRADE=1 apt-get install -y --allow-downgrades "$OLD_DEB"
expect_active
wait_for_version "$OLD_VERSION"

step "après un self-update, le paquet de l'ancienne version ne repasse pas en douce"
# self-update laisse la base dpkg en arrière : dpkg croit OLD_VERSION alors que
# le binaire est en NEW_VERSION. C'est le binaire qui fait foi.
dpkg-deb -x "$NEW_DEB" "$WORK_DIR/new"
replace_binary_as_self_update_would "$WORK_DIR/new$BIN_PATH"
wait_for_version "$NEW_VERSION"
[ "$(dpkg-query -W -f '${Version}' opencloud)" = "$OLD_VERSION" ] || fail "dpkg devrait être resté en $OLD_VERSION"
if apt-get install -y --reinstall "$OLD_DEB" > "$WORK_DIR/reinstall.log" 2>&1; then
    fail "la réinstallation de $OLD_VERSION est passée alors que le binaire est en $NEW_VERSION"
fi
grep -q "refuse de rétrograder" "$WORK_DIR/reinstall.log" || fail "le refus ne se nomme pas : $(cat "$WORK_DIR/reinstall.log")"
[ "$(opencloud version)" = "opencloud $NEW_VERSION" ] || fail "le binaire a reculé en silence"

step "un .lock ou un .new orphelin ne survit pas à une pose du paquet"
# Laissés par un self-update interrompu : le .lock est son verrou, le .new son
# fichier d'écriture ; l'un comme l'autre bloquerait toutes les mises à jour
# suivantes.
: > "$BIN_PATH.new"
: > "$BIN_PATH.lock"
apt-get install -y "$NEW_DEB"
[ ! -e "$BIN_PATH.new" ] || fail "postinst a laissé le .new orphelin, tout self-update ultérieur serait bloqué"
[ ! -e "$BIN_PATH.lock" ] || fail "postinst a laissé le .lock orphelin, tout self-update ultérieur serait bloqué"
expect_active
wait_for_version "$NEW_VERSION"
[ "$(dpkg-query -W -f '${Version}' opencloud)" = "$NEW_VERSION" ] || fail "dpkg ne voit pas la nouvelle version"

step "un systemd qui refuse ne fait pas échouer le retrait"
# Comme dh_installsystemd : un stop ou un daemon-reload en échec laisserait
# sinon le paquet à demi retiré, sans binaire et toujours déclaré installé.
# On joue les scripts que dpkg a posés, avec un systemd qui dit non à tout.
mkdir -p "$WORK_DIR/stub"
for stubbed in deb-systemd-invoke deb-systemd-helper systemctl; do
    printf '%s\n' '#!/bin/sh' 'exit 1' > "$WORK_DIR/stub/$stubbed"
    chmod 0755 "$WORK_DIR/stub/$stubbed"
done
PATH="$WORK_DIR/stub:$PATH" /var/lib/dpkg/info/opencloud.prerm remove \
    || fail "prerm échoue quand l'arrêt échoue"
PATH="$WORK_DIR/stub:$PATH" /var/lib/dpkg/info/opencloud.postrm remove \
    || fail "postrm échoue quand daemon-reload échoue"
expect_active

step "apt remove : le binaire et l'unité partent, l'état reste"
# Un verrou de self-update resté là n'appartient pas à dpkg : postrm le retire,
# sinon /opt/opencloud resterait après la purge.
: > "$BIN_PATH.lock"
apt-get remove -y opencloud
systemctl is-active --quiet opencloud.service && fail "l'unité tourne encore"
[ ! -e "$BIN_PATH.lock" ] || fail "apt remove a laissé le verrou de self-update"
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
