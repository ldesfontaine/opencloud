# shellcheck shell=bash
# Supprimer un hôte virtuel : retirer le fragment du nom. Le proxy relit son
# dossier tout seul, il n'y a rien à recharger — et le nom cesse d'être servi,
# d'où la confirmation à l'écran « avant » (05-execution.md).
#
# Le certificat n'est pas touché : il vit dans le stockage ACME du proxy, et
# c'est l'action « Demander un certificat » qui en répondra. L'en-tête commun
# (lib.sh) est concaténé avant ce corps.

FRAGMENTS_DIR=/srv/data/traefik
PROXY_SERVICE_DIR=/srv/workspace/system/traefik
PROXY_CONTAINER_NAME=traefik
SECURE_PORT=443

# Le proxy relit son dossier tout seul : quinze essais à une seconde lui
# laissent le temps sans tenir la machine.
CHECK_ATTEMPTS=15
CHECK_TIMEOUT_SECONDS=5

DOCKER=/usr/bin/docker
CURL=/usr/bin/curl

set -E
trap 'fail "une commande de la séquence a échoué, ligne $LINENO"' ERR

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant de retirer quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette action écrit dans la configuration du proxy, et elle ne tourne pas en root" \
        "relancer l'action depuis openCloud : le lanceur l'élève par systemd-run"
fi
if [ ! -x "$CURL" ]; then
    refuse "$CURL est absent : la vérification finale passe par une vraie requête" \
        "lancer « Poser le socle » sur cette machine, puis relancer l'action"
fi
if [ ! -f "$PROXY_SERVICE_DIR/compose.yaml" ] || [ ! -d "$FRAGMENTS_DIR" ]; then
    refuse "cette machine ne tient aucun proxy : il n'y a aucune route à retirer" \
        "vérifier la machine : « Installer le proxy » n'y a jamais été jouée"
fi

FRAGMENT="$FRAGMENTS_DIR/$OC_DOMAIN.yml"

# --------------------------------------------------------------- 1. retirer

step "retirer le fragment de $OC_DOMAIN"

if [ ! -e "$FRAGMENT" ]; then
    step "$FRAGMENT — inchangé, il n'y était pas"
    info fragment absent
    done_unchanged
fi

rm -f -- "$FRAGMENT"
step "$FRAGMENT retiré"
info fragment retire

# -------------------------------------------------------------- 2. vérifier

step "vérifier par le chemin réel — le nom n'est plus servi"

# Le proxy est peut-être arrêté : dans ce cas le fragment est bien parti, et
# rien ne peut répondre. C'est un constat, pas un échec.
if [ "$("$DOCKER" container inspect --format '{{.State.Status}}' -- "$PROXY_CONTAINER_NAME" 2> /dev/null || true)" != "running" ]; then
    warn "le proxy de cette machine ne tourne pas : le fragment est retiré, mais rien n'a pu être vérifié"
    done_changed
fi

code=""
for _ in $(seq 1 "$CHECK_ATTEMPTS"); do
    code=$("$CURL" -sS -k -o /dev/null -w '%{http_code}' \
        --max-time "$CHECK_TIMEOUT_SECONDS" \
        --resolve "$OC_DOMAIN:$SECURE_PORT:127.0.0.1" \
        "https://$OC_DOMAIN/" 2> /dev/null || true)
    if [ "$code" = "404" ]; then
        break
    fi
    sleep 1
done
info code "${code:-sans réponse}"
if [ "$code" != "404" ]; then
    fail "le proxy sert encore $OC_DOMAIN : il répond « ${code:-rien} » au lieu de 404"
fi

warn "le certificat de $OC_DOMAIN reste dans le stockage ACME du proxy : il n'est pas révoqué"

# ------------------------------------------------------------------ 3. constat

done_changed
