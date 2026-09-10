# shellcheck shell=bash
# Poser le jeton DNS : le jeton Cloudflare de la zone, à côté des certificats
# qu'il sert à obtenir. Le fichier est rendu par Go et déposé sous files/ ; ce
# script le pose, il ne le compose pas et ne l'affiche jamais. L'en-tête commun
# (lib.sh) est concaténé avant ce corps. Le script tourne en root : oc-launch
# le lance par systemd-run.
#
# Le jeton n'apparaît nulle part : ni dans une ligne de commande, ni dans un
# journal, ni dans un message. Ce que le script en dit, c'est l'empreinte
# SHA-256 tronquée du fichier posé — assez pour relier la pose à une zone,
# rien pour s'en servir. « set -x » est donc interdit ici.
#
# La séquence : refus si le proxy n'est pas posé ou ne tourne pas ; comparer
# octet pour octet ; poser atomiquement en 0600 root ; si le fichier a changé,
# redémarrer le proxy — lego lit le jeton au démarrage du résolveur — puis
# vérifier qu'il est revenu et que son journal ne dit rien sur le résolveur.

FILES_DIR=$(cd -- "$(dirname -- "$0")" && pwd)/files
TOKEN_SOURCE="$FILES_DIR/cloudflare.token"

# Ce que le proxy occupe sur la machine (« Installer le proxy »).
PROXY_SERVICE_DIR=/srv/workspace/system/traefik
PROXY_COMPOSE="$PROXY_SERVICE_DIR/compose.yaml"
PROXY_CONTAINER_NAME=traefik
ACME_DIR=/srv/data/acme
TOKEN_TARGET="$ACME_DIR/cloudflare.token"
SECURE_PORT=443

TOKEN_MODE=0600
TOKEN_OWNER=root
TOKEN_GROUP=root

# L'empreinte que l'observateur relit : douze caractères hexadécimaux.
FINGERPRINT_LENGTH=12

# Un nom que rien ne route : .invalid est réservé (RFC 2606). C'est lui qui
# doit recevoir un 404 du proxy revenu.
UNKNOWN_NAME=proxy-inconnu.invalid
# Traefik s'arrête, redémarre, relit sa configuration et rouvre ses ports :
# quinze essais à une seconde couvrent large sans tenir la machine.
CHECK_ATTEMPTS=15
CHECK_TIMEOUT_SECONDS=5
# La fenêtre de journal relue après le redémarrage : ce que le proxy a dit
# depuis qu'il est reparti, et rien d'avant.
LOG_WINDOW=2m
LOG_LINES_SHOWN=5

DOCKER=/usr/bin/docker
MAKE=/usr/bin/make
CURL=/usr/bin/curl
INSTALL=/usr/bin/install
SHA256SUM=/usr/bin/sha256sum

set -E
trap 'fail "une commande de la séquence a échoué, ligne $LINENO"' ERR

changed=0

step_done() {
    changed=1
    step "$*"
}

step_unchanged() {
    step "$* — inchangé"
}

require_binary() {
    local path=$1 reason=$2
    if [ ! -x "$path" ]; then
        refuse "$path est absent : $reason" \
            "lancer « Poser le socle » sur cette machine, puis relancer l'action"
    fi
}

# L'état d'un conteneur, vide s'il n'existe pas.
container_status() {
    "$DOCKER" container inspect --format '{{.State.Status}}' -- "$1" 2> /dev/null || true
}

# L'empreinte d'un fichier, tronquée. Le jeton entre par un tube, il ne passe
# ni par une variable ni par une ligne de commande.
fingerprint_of() {
    "$SHA256SUM" < "$1" | cut -c "1-$FINGERPRINT_LENGTH"
}

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant d'écrire quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette action écrit un secret lisible du seul root, et elle ne tourne pas en root" \
        "relancer l'action depuis openCloud : le lanceur l'élève par systemd-run"
fi

require_binary "$DOCKER" "le proxy tourne en conteneur"
require_binary "$MAKE" "les cibles du service sont celles de son Makefile"
require_binary "$CURL" "la vérification finale passe par une vraie requête"
require_binary "$INSTALL" "install pose le fichier avec son mode et son propriétaire"
require_binary "$SHA256SUM" "l'empreinte relie la pose à la zone"

if [ ! -f "$TOKEN_SOURCE" ]; then
    fail "le jeton n'a pas été déposé avec l'action"
fi
if [ ! -s "$TOKEN_SOURCE" ]; then
    fail "le jeton déposé avec l'action est vide"
fi

# Le proxy : son service, son dossier de certificats, son conteneur. Les trois,
# parce que chacun manque pour une raison différente.
if [ ! -f "$PROXY_COMPOSE" ] || [ ! -d "$ACME_DIR" ]; then
    refuse "cette machine ne tient aucun proxy : le jeton DNS est lu par Traefik, il ne se pose pas avant lui" \
        "lancer « Installer le proxy » sur cette machine, puis relancer l'action"
fi
proxy_status=$(container_status "$PROXY_CONTAINER_NAME")
if [ "$proxy_status" != "running" ]; then
    refuse "le proxy de cette machine ne tourne pas : il est ${proxy_status:-absent}" \
        "relancer « Installer le proxy » sur cette machine, puis relancer l'action"
fi
info proxy "$proxy_status"
info zone "$OC_ZONE"

# ------------------------------------------------------------- 1. le fichier

step "poser le jeton de la zone $OC_ZONE"

fingerprint=$(fingerprint_of "$TOKEN_SOURCE")

if [ -f "$TOKEN_TARGET" ] && cmp -s -- "$TOKEN_SOURCE" "$TOKEN_TARGET"; then
    step_unchanged "$TOKEN_TARGET"
else
    # Le temporaire naît dans le dossier de destination, en 0600 dès sa
    # création : le rename est atomique, et le jeton n'est jamais lisible d'un
    # tiers, même une fraction de seconde.
    "$INSTALL" -o "$TOKEN_OWNER" -g "$TOKEN_GROUP" -m "$TOKEN_MODE" -- \
        "$TOKEN_SOURCE" "$TOKEN_TARGET.opencloud-tmp"
    mv -f -- "$TOKEN_TARGET.opencloud-tmp" "$TOKEN_TARGET"
    step_done "$TOKEN_TARGET écrit en $TOKEN_MODE, $TOKEN_OWNER:$TOKEN_GROUP"
fi

# L'empreinte, jamais le jeton : c'est elle qui relie cette pose à sa zone.
info jeton "$fingerprint"
info fichier "$TOKEN_TARGET"

# ------------------------------------------------------------ 2. le proxy

if [ "$changed" -eq 0 ]; then
    step_unchanged "le proxy garde le jeton qu'il a déjà : rien à redémarrer"
    done_unchanged
fi

step "redémarrer le proxy — lego lit le jeton au démarrage du résolveur"

if ! restart_output=$("$MAKE" -C "$PROXY_SERVICE_DIR" restart 2>&1); then
    printf '%s\n' "$restart_output" | tail -n 20
    fail "make restart n'a pas relancé le proxy"
fi

# ------------------------------------------------------------- 3. vérifier

step "vérifier par le chemin réel — le proxy répond à nouveau"

# Un nom que rien ne route doit recevoir un 404 : le proxy sert, il ne connaît
# rien de ce nom. -k : ce n'est pas le certificat qu'on éprouve ici.
secure_code=""
for _ in $(seq 1 "$CHECK_ATTEMPTS"); do
    secure_code=$("$CURL" -sS -k -o /dev/null -w '%{http_code}' \
        --max-time "$CHECK_TIMEOUT_SECONDS" \
        --resolve "$UNKNOWN_NAME:$SECURE_PORT:127.0.0.1" \
        "https://$UNKNOWN_NAME/" 2> /dev/null || true)
    if [ "$secure_code" = "404" ]; then
        break
    fi
    sleep 1
done
info "port_${SECURE_PORT}" "${secure_code:-sans réponse}"
if [ "$secure_code" != "404" ]; then
    fail "le proxy ne répond pas 404 à un nom inconnu sur $SECURE_PORT après le redémarrage : « ${secure_code:-rien} »"
fi

step "relire le journal du proxy sur le résolveur"

# Ce que le proxy a dit depuis son redémarrage, sur l'ACME et sur Cloudflare.
# Traefik n'écrit jamais le jeton dans son journal ; on ne remonte de toute
# façon que les lignes d'erreur, et quelques-unes.
if ! resolver_log=$("$DOCKER" compose --project-directory "$PROXY_SERVICE_DIR" \
    -f "$PROXY_COMPOSE" logs --no-color --since "$LOG_WINDOW" 2>&1); then
    warn "le journal du proxy n'a pas pu être relu : le résolveur n'a pas été vérifié"
else
    resolver_errors=$(printf '%s\n' "$resolver_log" \
        | grep -i -e 'acme' -e 'cloudflare' \
        | grep -i -e 'level=error' -e '"level":"error"' || true)
    if [ -n "$resolver_errors" ]; then
        printf '%s\n' "$resolver_errors" | tail -n "$LOG_LINES_SHOWN"
        fail "le proxy signale une erreur sur son résolveur DNS après la pose du jeton"
    fi
    step "le journal du proxy ne signale rien sur le résolveur"
fi

# ------------------------------------------------------------------ 4. constat

done_changed
