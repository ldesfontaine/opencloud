#!/bin/bash
# Test de bout en bout d'une action, sur une machine jetable avec systemd — le
# runner de la CI ou le conteneur —, jamais sur un poste de travail : installer
# le paquet, amorcer la machine openCloud sur elle-même, lancer Diagnostiquer
# depuis l'interface, la suivre, relire journald, couper openCloud au milieu.
# usage : sudo packaging/test-action.sh <opencloud.deb>
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive

DEB=$(readlink -f -- "$1")
VERSION=$(dpkg-deb -f "$DEB" Version)
BASE=http://127.0.0.1:8080
# Un mot de passe de test, fabriqué ici : rien en dur qu'un scanner prendrait
# pour un secret. Douze caractères au moins, la règle du binaire.
PASSWORD="jetable-$(date +%s)-diagnostiquer"
WORK_DIR=$(mktemp -d -p /var/tmp)
COOKIES=$WORK_DIR/cookies

# shellcheck source=packaging/test-lib.sh
. "$(dirname -- "$0")/test-lib.sh"

step "installation du paquet $VERSION"
apt-get install -y "$DEB" > "$WORK_DIR/install.log" 2>&1 || { cat "$WORK_DIR/install.log"; fail "apt install"; }
wait_for_health
grep -q "enroll-local" "$WORK_DIR/install.log" || fail "l'installation ne dit pas comment amorcer la machine"

step "connexion, changement du mot de passe par défaut"
login_and_set_password

step "avant l'amorçage : la machine se dit non enrôlée, une action est refusée"
page_has /machines/local "non enrôlée" || fail "la fiche ne dit pas « non enrôlée »"
id=$(launch_diagnostiquer local)
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Refusée' || fail "une action vers une machine non enrôlée doit être refusée"
printf '%s' "$page" | grep -q 'enroll-local' || fail "le refus ne donne pas le geste qui le lève"

step "amorçage : opencloud enroll-local"
opencloud enroll-local > "$WORK_DIR/enroll-1.log" 2>&1 || { cat "$WORK_DIR/enroll-1.log"; fail "enroll-local"; }
cat "$WORK_DIR/enroll-1.log"
grep -q '^résultat: fait$' "$WORK_DIR/enroll-1.log" || fail "le premier amorçage ne dit pas « fait »"
[ -f /usr/local/sbin/oc-launch ] || fail "le lanceur n'a pas été posé"
[ "$(stat -c '%U:%G %a' /usr/local/sbin/oc-launch)" = "root:root 755" ] || fail "le lanceur n'est pas root:root 0755"
[ "$(stat -c '%U:%G %a' /etc/sudoers.d/opencloud)" = "root:root 440" ] || fail "la règle sudo n'est pas root:root 0440"
[ "$(stat -c '%U %a' /var/lib/opencloud/machines/local/id_ed25519)" = "opencloud 600" ] || fail "la clé n'est pas opencloud 0600"
id -nG opencloud | tr ' ' '\n' | grep -qx systemd-journal || fail "le compte opencloud n'est pas dans systemd-journal"

step "rejoué : tout est inchangé"
opencloud enroll-local > "$WORK_DIR/enroll-2.log" 2>&1 || { cat "$WORK_DIR/enroll-2.log"; fail "second enroll-local"; }
grep -q '^résultat: inchangé$' "$WORK_DIR/enroll-2.log" || { cat "$WORK_DIR/enroll-2.log"; fail "le second amorçage ne dit pas « inchangé »"; }

step "sudo n'ouvre que le lanceur au compte opencloud, et le lanceur refuse un identifiant sans dossier"
set +e
runuser -u opencloud -- sudo -n /usr/local/sbin/oc-launch explode > "$WORK_DIR/explode.log" 2>&1
code=$?
set -e
[ "$code" -eq 2 ] || { cat "$WORK_DIR/explode.log"; fail "oc-launch explode sort $code, attendu 2"; }
set +e
runuser -u opencloud -- sudo -n /bin/true > "$WORK_DIR/sudo-true.log" 2>&1
code=$?
set -e
[ "$code" -ne 0 ] || fail "sudo laisse le compte opencloud lancer autre chose que le lanceur"

step "la fiche de la machine dit enrôlée"
page_has /machines/local "enrôlée depuis" \
    || fail "la fiche ne dit pas « enrôlée depuis » : $(get_page /machines/local | grep -o 'enrôlée[^<]*' | head -n 1)"

step "Diagnostiquer, suivie jusqu'à sa conclusion"
id=$(launch_diagnostiquer local)
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Diagnostiquer n'est pas « Appliquée »"; }
printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "la page ne montre pas « résultat: inchangé »"
printf '%s' "$page" | grep -q 'info: hote=' || fail "la page ne montre pas les constats du script"
journalctl -u "oc-action-$id" --no-pager | grep -q 'résultat: inchangé' || fail "journald ne porte pas la sortie de l'unité"
[ "$(stat -c '%U %a' "/var/lib/opencloud/actions/$id/run.sh")" = "opencloud 755" ] || fail "run.sh n'est pas opencloud 0755"
[ "$(stat -c '%U %a' "/var/lib/opencloud/actions/$id/params.env")" = "opencloud 600" ] || fail "params.env n'est pas opencloud 0600"

step "le direct d'une action conclue rejoue et se termine"
curl -sS -N --max-time 5 -b "$COOKIES" "$BASE/actions/$id/stream" > "$WORK_DIR/stream.log" || true
grep -q '^event: line' "$WORK_DIR/stream.log" || fail "le flux ne rejoue pas les lignes"
grep -q '^event: done' "$WORK_DIR/stream.log" || fail "le flux ne se termine pas par « done »"

step "rejouée : inchangé la seconde fois aussi"
id=$(launch_diagnostiquer local)
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Appliquée' || fail "la seconde Diagnostiquer n'est pas « Appliquée »"
printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "la seconde Diagnostiquer ne dit pas « inchangé »"

step "openCloud coupé pendant une action : la reprise conclut sans intervention"
id=$(launch_diagnostiquer local)
systemctl restart opencloud.service
wait_for_health
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Appliquée' || fail "l'action lancée avant la coupure n'est pas « Appliquée » après la reprise"

step "l'historique de la machine montre les actions"
page_has /machines/local "$id" || fail "la fiche de la machine ne liste pas la dernière action"

printf '\nTout tient : amorçage, refus, lancement, suivi, journal, direct, reprise.\n'
