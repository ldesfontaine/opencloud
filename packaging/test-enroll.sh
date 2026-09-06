#!/bin/bash
# Test de bout en bout de l'enrôlement d'une machine distante, sur une machine
# jetable avec systemd et Docker — le runner de la CI —, jamais sur un poste de
# travail : le témoin (conteneur Debian avec sshd) est déclaré depuis
# l'interface, la commande générée est jouée dedans, l'empreinte saisie,
# Diagnostiquer lancée dessus par SSH, puis le témoin est éteint.
# usage : sudo packaging/test-enroll.sh <opencloud.deb>
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive

DEB=$(readlink -f -- "$1")
VERSION=$(dpkg-deb -f "$DEB" Version)
BASE=http://127.0.0.1:8080
PASSWORD="jetable-$(date +%s)-enrolement"
WORK_DIR=$(mktemp -d -p /var/tmp)
COOKIES=$WORK_DIR/cookies
TEMOIN=opencloud-temoin
TEMOIN_PORT=2222
IMAGE_DIR=$(dirname -- "$(readlink -f -- "$0")")/test-image

# shellcheck source=packaging/test-lib.sh
. "$(dirname -- "$0")/test-lib.sh"

cleanup() { docker rm -f "$TEMOIN" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Ce que le HTML a échappé dans le textarea, remis en clair : html/template
# échappe aussi « + » et « = », qui vivent dans le base64.
unescape_html() {
    sed -e "s/&#39;/'/g" -e 's/&#34;/"/g' -e 's/&#43;/+/g' -e 's/&#61;/=/g' -e 's/&lt;/</g' -e 's/&gt;/>/g' -e 's/&amp;/\&/g'
}

step "installation du paquet $VERSION et amorçage de la machine openCloud"
# Un autre test a pu laisser openCloud installé, avec son mot de passe changé
# et sa machine amorcée : on repart d'un état vierge.
if dpkg -s opencloud > /dev/null 2>&1; then
    apt-get purge -y opencloud > /dev/null 2>&1 || fail "apt purge de l'installation précédente"
fi
apt-get install -y "$DEB" > "$WORK_DIR/install.log" 2>&1 || { cat "$WORK_DIR/install.log"; fail "apt install"; }
wait_for_health
opencloud enroll-local > "$WORK_DIR/enroll-local.log" 2>&1 || { cat "$WORK_DIR/enroll-local.log"; fail "enroll-local"; }

step "le témoin : une Debian jetable avec sshd, sur 127.0.0.1:$TEMOIN_PORT"
docker build -q -t opencloud-package-test "$IMAGE_DIR" > /dev/null
docker rm -f "$TEMOIN" > /dev/null 2>&1 || true
docker run -d --name "$TEMOIN" --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
    -p "127.0.0.1:$TEMOIN_PORT:22" opencloud-package-test > /dev/null
for _ in $(seq 1 30); do
    docker exec "$TEMOIN" systemctl is-system-running > /dev/null 2>&1 && break
    sleep 1
done

step "connexion, changement du mot de passe par défaut"
login_and_set_password

step "déclarer le témoin depuis l'interface"
token=$(csrf_of /machines/new)
[ -n "$token" ] || fail "pas de jeton CSRF sur le formulaire de déclaration"
reply=$(post_form /machines --data-urlencode "_csrf=$token" --data-urlencode "name=temoin" \
    --data-urlencode "address=127.0.0.1" --data-urlencode "port=$TEMOIN_PORT")
[ "$reply" = "303 $BASE/machines/temoin" ] || fail "la déclaration n'a pas redirigé vers la fiche : $reply"
page=$(get_page /machines/temoin)
printf '%s' "$page" | grep -q "non enrôlée" || fail "la fiche ne dit pas « non enrôlée »"
command=$(printf '%s' "$page" | tr '\n' ' ' | sed -n 's/.*<textarea[^>]*>\([^<]*\)<\/textarea>.*/\1/p' | unescape_html)
case "$command" in
    "echo '"*"' | base64 -d | sudo bash"*) ;;
    *) fail "la commande à coller n'a pas la forme attendue : ${command:0:80}" ;;
esac

step "une empreinte qui ne correspond à rien est refusée avant tout"
token=$(csrf_of /machines/temoin)
reply=$(post_form /machines/temoin/confirm --data-urlencode "_csrf=$token" \
    --data-urlencode "fingerprint=SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
case "$reply" in 422*) ;; *) fail "une empreinte inconnue doit être refusée (422), reçu : $reply" ;; esac

step "la commande jouée dans le témoin, en root"
docker exec -i "$TEMOIN" bash -c "$command" > "$WORK_DIR/enroll-1.log" 2>&1 || { cat "$WORK_DIR/enroll-1.log"; fail "la commande d'enrôlement a échoué"; }
cat "$WORK_DIR/enroll-1.log"
grep -q '^résultat: fait$' "$WORK_DIR/enroll-1.log" || fail "le premier enrôlement ne dit pas « fait »"
fingerprint=$(sed -n 's/^info: empreinte_ed25519=\(SHA256:[A-Za-z0-9+\/]*\).*/\1/p' "$WORK_DIR/enroll-1.log" | head -n 1)
[ -n "$fingerprint" ] || fail "la commande n'a pas affiché l'empreinte ed25519"

step "rejouée : tout est inchangé"
docker exec -i "$TEMOIN" bash -c "$command" > "$WORK_DIR/enroll-2.log" 2>&1 || { cat "$WORK_DIR/enroll-2.log"; fail "le second enrôlement a échoué"; }
grep -q '^résultat: inchangé$' "$WORK_DIR/enroll-2.log" || { cat "$WORK_DIR/enroll-2.log"; fail "le second enrôlement ne dit pas « inchangé »"; }

step "l'empreinte saisie : known_hosts écrit, lanceur posé par SSH, machine enrôlée"
token=$(csrf_of /machines/temoin)
reply=$(post_form /machines/temoin/confirm --data-urlencode "_csrf=$token" --data-urlencode "fingerprint=$fingerprint")
[ "$reply" = "303 $BASE/machines/temoin" ] || fail "la confirmation a échoué : $reply $(get_page /machines/temoin | grep -o 'Refus[^<]*' | head -n 1)"
get_page /machines/temoin | grep -q "enrôlée depuis" || fail "la fiche ne dit pas « enrôlée depuis »"
[ "$(docker exec "$TEMOIN" stat -c '%U:%G %a' /usr/local/sbin/oc-launch)" = "root:root 755" ] || fail "le lanceur n'est pas posé root:root 0755 sur le témoin"
docker exec "$TEMOIN" stat -c '%U %a' /var/lib/opencloud/.ssh/authorized_keys | grep -q '^opencloud 600$' || fail "authorized_keys n'est pas opencloud 0600"
grep -q "^\[127.0.0.1\]:$TEMOIN_PORT " /var/lib/opencloud/machines/temoin/known_hosts || fail "known_hosts ne porte pas la clé du témoin"

step "Diagnostiquer sur le témoin, par SSH, suivie jusqu'à sa conclusion"
id=$(launch_diagnostiquer temoin)
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Diagnostiquer sur le témoin n'est pas « Appliquée »"; }
printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "la page ne montre pas « résultat: inchangé »"
temoin_host=$(docker exec "$TEMOIN" uname -n)
printf '%s' "$page" | grep -q "info: hote=$temoin_host" || fail "la sortie ne vient pas du témoin ($temoin_host)"
docker exec "$TEMOIN" journalctl -u "oc-action-$id" --no-pager | grep -q 'résultat: inchangé' || fail "journald du témoin ne porte pas la sortie"

step "tester l'accès : joignable"
token=$(csrf_of /machines/temoin)
reply=$(post_form /machines/temoin/probe --data-urlencode "_csrf=$token")
case "$reply" in 303*) ;; *) fail "tester l'accès a échoué : $reply" ;; esac
get_page /machines/temoin | grep -q "joignable" || fail "la fiche ne dit pas « joignable »"

step "le témoin éteint passe « SSH en échec », jamais « en ligne »"
docker stop "$TEMOIN" > /dev/null
token=$(csrf_of /machines/temoin)
post_form /machines/temoin/probe --data-urlencode "_csrf=$token" > /dev/null
page=$(get_page /machines/temoin)
printf '%s' "$page" | grep -q "SSH en échec" || fail "la fiche ne dit pas « SSH en échec » après l'extinction"
printf '%s' "$page" | grep -qi "en ligne" && fail "la fiche dit « en ligne » sur une machine éteinte"
get_page / | grep -q "SSH en échec" || fail "l'Infrastructure ne montre pas l'état du témoin"

printf '\nTout tient : déclaration, commande, empreinte, lanceur, Diagnostiquer à distance, sonde, extinction.\n'
