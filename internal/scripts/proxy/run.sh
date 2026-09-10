# shellcheck shell=bash
# Installer le proxy : Traefik sur cette machine, un service sous la norme
# (03-modele.md) avec son compose.yaml et son Makefile. Les fichiers sont
# rendus par Go et déposés sous files/ ; ce script les pose, il n'en compose
# aucun. L'en-tête commun (lib.sh) est concaténé avant ce corps. Le script
# tourne en root : oc-launch le lance par systemd-run.
#
# L'ordre est une propriété de l'action (15-catalogue-actions.md §3) : tirer
# l'image par digest avant qu'un fichier la nomme, créer les répertoires avant
# qu'un montage les nomme, écrire la configuration avant que le service qui la
# lit démarre, puis vérifier par le chemin réel.

# Le dossier de l'action, d'où le lanceur a exécuté ce script, et les fichiers
# que Go y a rendus.
FILES_DIR=$(cd -- "$(dirname -- "$0")" && pwd)/files

# La norme /srv : le proxy est partagé par les environnements de la machine, il
# ne vit donc dans aucun d'eux.
WORKSPACE_DIR=/srv/workspace
SYSTEM_DIR=/srv/workspace/system
SERVICE_DIR=/srv/workspace/system/traefik
COMMON_MAKEFILE=/srv/workspace/Makefile.common
DATA_DIR=/srv/data
# Les fragments d'hôtes virtuels, vides tant qu'aucun domaine n'est publié.
FRAGMENTS_DIR=/srv/data/traefik
# Les certificats, leurs clés privées et le jeton DNS de la zone : fermé.
ACME_DIR=/srv/data/acme
ACME_DIR_MODE=0700

DIR_MODE=0755
DIR_OWNER=root
DIR_GROUP=root
FILE_MODE=0644

CONTAINER_NAME=traefik
CLEAR_PORT=80
SECURE_PORT=443
# Un nom que rien ne route : .invalid est réservé (RFC 2606). C'est lui qui
# doit recevoir un 404 du proxy, preuve qu'il sert sans rien connaître.
UNKNOWN_NAME=proxy-inconnu.invalid
# Traefik démarre, lit sa configuration et ouvre ses ports : quinze essais à
# une seconde couvrent large sans tenir la machine.
CHECK_ATTEMPTS=15
CHECK_TIMEOUT_SECONDS=5

# Le fichier-garde du socle : sans lui, ni Docker ni la norme /srv.
SOCLE_GUARD_FILE=/var/lib/opencloud/socle.liste

DOCKER=/usr/bin/docker
MAKE=/usr/bin/make
CURL=/usr/bin/curl
INSTALL=/usr/bin/install
# L'image de Traefik pèse quelques centaines de mébioctets déballée, et
# /var/lib/docker vit sur /var.
MIN_FREE_MIB=1024

# set -e sort sans rien dire, et le contrat veut une ligne de constat. set -E
# porte le piège dans les fonctions, où la moitié des commandes vivent.
set -E
trap 'fail "une commande de la séquence a échoué, ligne $LINENO"' ERR

# Ce que la machine a bougé : « inchangé » n'est vrai que si rien n'a changé.
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
            "poser le socle sur cette machine, puis relancer l'action"
    fi
}

# L'espace libre d'un point de montage, en mébioctets.
free_space_mib() {
    local path=$1 blocks
    blocks=$(df -P -- "$path" | awk 'NR == 2 { print $4 }')
    printf '%s' "$((blocks / 1024))"
}

# L'inode de la socket qui écoute sur un port, s'il y en a une. Lu dans /proc :
# iproute2 n'est pas garanti sur une machine neuve, et un refus qui ne sait pas
# regarder ne vaut rien. Colonne 2 : l'adresse locale « <adresse>:<port> » en
# hexadécimal ; colonne 4 : 0A pour « écoute » ; colonne 10 : l'inode.
listening_inode() {
    local port=$1 hex_port
    hex_port=$(printf '%04X' "$port")
    awk -v suffix=":$hex_port" \
        '$4 == "0A" && substr($2, length($2) - 4) == suffix { print $10; exit }' \
        /proc/net/tcp /proc/net/tcp6 2> /dev/null || true
}

# Qui tient cette socket : le processus dont un descripteur pointe sur l'inode.
# Le refus nomme le processus, pas seulement le port (15-catalogue-actions.md
# §5) — « port pris » ne dit pas quoi faire.
process_of_inode() {
    local inode=$1 entry pid link name
    for entry in /proc/[0-9]*; do
        pid=${entry#/proc/}
        for link in "$entry"/fd/*; do
            [ -e "$link" ] || continue
            if [ "$(readlink -- "$link" 2> /dev/null || true)" = "socket:[$inode]" ]; then
                name=$(cat -- "$entry/comm" 2> /dev/null || true)
                printf '%s (pid %s)' "${name:-inconnu}" "$pid"
                return 0
            fi
        done
    done
    return 1
}

refuse_port_taken() {
    local port=$1 inode=$2 holder
    holder=$(process_of_inode "$inode" || printf 'un processus que /proc ne nomme plus')
    refuse "le port $port est déjà tenu par $holder : le proxy est la porte de la machine, il ne partage pas ses ports" \
        "arrêter ce qui tient le port $port, puis relancer l'action"
}

# L'état du conteneur du proxy, vide s'il n'existe pas.
container_status() {
    "$DOCKER" container inspect --format '{{.State.Status}}' -- "$CONTAINER_NAME" 2> /dev/null || true
}

# Vrai si le fichier posé est déjà exactement celui que Go a rendu.
file_is_current() {
    local source=$1 target=$2
    [ -f "$target" ] && cmp -s -- "$source" "$target"
}

# Pose un fichier rendu par Go. Écriture atomique : le temporaire naît dans le
# même dossier, puis un rename — Traefik relit ces fichiers quand il veut, il
# ne doit jamais en voir un à moitié écrit.
place_file() {
    local name=$1 target=$2
    local source="$FILES_DIR/$name"

    if [ ! -f "$source" ]; then
        fail "le fichier « $name » n'a pas été déposé avec l'action"
    fi
    if file_is_current "$source" "$target"; then
        step_unchanged "$target"
        return 0
    fi
    "$INSTALL" -o "$DIR_OWNER" -g "$DIR_GROUP" -m "$FILE_MODE" -- "$source" "$target.opencloud-tmp"
    mv -f -- "$target.opencloud-tmp" "$target"
    step_done "$target écrit"
}

# La norme prime, mais openCloud ne possède pas /srv : il crée ce qui manque et
# ne touche à rien d'autre (04-implantation.md).
ensure_directory() {
    local path=$1 mode=$2
    if [ -e "$path" ] && [ ! -d "$path" ]; then
        refuse "$path existe et n'est pas un répertoire : le proxy en attend un" \
            "déplacer ou renommer $path, puis relancer l'action"
    fi
    if [ -d "$path" ]; then
        step_unchanged "$path"
        return 0
    fi
    "$INSTALL" -d -o "$DIR_OWNER" -g "$DIR_GROUP" -m "$mode" -- "$path"
    step_done "$path posé en $mode, $DIR_OWNER:$DIR_GROUP"
}

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant d'écrire quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette action pose un service système et ouvre les ports 80 et 443, et elle ne tourne pas en root" \
        "relancer l'action depuis openCloud : le lanceur l'élève par systemd-run"
fi
if [ ! -d /run/systemd/system ]; then
    refuse "cette machine ne tourne pas sous systemd" \
        "installer le proxy sur une machine gérée par systemd"
fi
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
    refuse "cette machine n'expose pas cgroup v2 : Docker et systemd-run en dépendent" \
        "démarrer le noyau avec systemd.unified_cgroup_hierarchy=1, puis relancer l'action"
fi

# Un gestionnaire concurrent porte déjà le proxy, le pare-feu et les
# certificats : openCloud ne cohabite pas avec lui (15-catalogue-actions.md §5).
if [ -d /usr/local/psa ]; then
    refuse "cette machine est déjà tenue par Plesk : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "installer le proxy sur une machine qu'openCloud est seul à gérer"
fi
if [ -d /usr/local/cpanel ]; then
    refuse "cette machine est déjà tenue par cPanel : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "installer le proxy sur une machine qu'openCloud est seul à gérer"
fi
if systemctl is-active --quiet certbot.timer 2> /dev/null; then
    refuse "un certbot est actif sur cette machine : deux clients ACME se disputeraient le renouvellement du même domaine" \
        "désactiver certbot.timer — Traefik renouvelle lui-même en DNS-01 — puis relancer l'action"
fi

if [ ! -f "$SOCLE_GUARD_FILE" ]; then
    refuse "le socle n'est pas posé sur cette machine : $SOCLE_GUARD_FILE est absent" \
        "lancer « Poser le socle » sur cette machine, puis relancer l'action"
fi
require_binary "$DOCKER" "le proxy tourne en conteneur"
require_binary "$MAKE" "les cibles du service sont celles de son Makefile"
require_binary "$CURL" "la vérification finale passe par une vraie requête"
require_binary "$INSTALL" "install pose les répertoires et les fichiers"

if ! compose_version=$("$DOCKER" compose version --short 2> /dev/null); then
    refuse "le plugin compose ne répond pas : « docker compose » est ce que les cibles du service appellent" \
        "lancer « Poser le socle » sur cette machine, puis relancer l'action"
fi
info docker_compose "$compose_version"

# Le démon, constaté par le chemin réel : un binaire présent ne dit rien de ce
# qui tourne.
if ! "$DOCKER" info > /dev/null 2>&1; then
    refuse "le démon Docker ne répond pas sur cette machine" \
        "démarrer le démon — systemctl start docker — puis relancer l'action"
fi

for path in "$WORKSPACE_DIR" "$DATA_DIR"; do
    if [ ! -d "$path" ]; then
        refuse "$path est absent : la norme d'implantation attend ce répertoire" \
            "lancer « Poser le socle » sur cette machine, puis relancer l'action"
    fi
done

free_mib=$(free_space_mib /var)
if [ "$free_mib" -lt "$MIN_FREE_MIB" ]; then
    refuse "cette machine n'a que $free_mib Mio libres sur /var, et l'image du proxy en demande $MIN_FREE_MIB" \
        "faire de la place sur /var — docker image prune, journalctl --vacuum-size — puis relancer l'action"
fi
info espace_libre_var "$free_mib Mio"

# Les ports du proxy. Rejouée, l'action retrouve ses propres ports tenus par le
# conteneur qu'elle a posé : ce n'est pas un conflit.
running_before=0
if [ "$(container_status)" = "running" ]; then
    running_before=1
    info ports "$CLEAR_PORT et $SECURE_PORT tenus par le proxy de cette machine"
else
    for port in "$CLEAR_PORT" "$SECURE_PORT"; do
        inode=$(listening_inode "$port")
        if [ -n "$inode" ]; then
            refuse_port_taken "$port" "$inode"
        fi
    done
    info ports "$CLEAR_PORT et $SECURE_PORT libres"
fi

# Une horloge en dérive fait rejeter les certificats et les jetons d'API. C'est
# un constat, pas un refus.
synchronized=$(timedatectl show -p NTPSynchronized --value 2> /dev/null || true)
info horloge_synchronisee "${synchronized:-inconnu}"

# ------------------------------------------------------------- 1. l'image

step "tirer l'image du proxy par son digest"

# La référence complète, rendue par Go et lue ici : le script tire l'image
# avant qu'un fichier posé la nomme.
image_file="$FILES_DIR/image"
if [ ! -f "$image_file" ]; then
    fail "la référence de l'image n'a pas été déposée avec l'action"
fi
image=$(head -n 1 -- "$image_file")
if [[ ! $image =~ ^[a-z0-9./_-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$ ]]; then
    fail "la référence « $image » ne nomme pas une image épinglée par digest"
fi

# Un digest est immuable : l'image déjà là est celle-là, il n'y a rien à
# retirer du réseau.
if "$DOCKER" image inspect --format '{{.Id}}' -- "$image" > /dev/null 2>&1; then
    step_unchanged "image $image"
else
    if ! pull_output=$("$DOCKER" image pull -- "$image" 2>&1); then
        printf '%s\n' "$pull_output" | tail -n 20
        fail "l'image $image n'a pas pu être tirée"
    fi
    step_done "image $image tirée"
fi
info image "$image"

# --------------------------------------------------------- 2. les répertoires

step "les répertoires du proxy"

ensure_directory "$SYSTEM_DIR" "$DIR_MODE"
ensure_directory "$SERVICE_DIR" "$DIR_MODE"
ensure_directory "$FRAGMENTS_DIR" "$DIR_MODE"
ensure_directory "$ACME_DIR" "$ACME_DIR_MODE"

# ------------------------------------------------------------ 3. les fichiers

step "la configuration statique et le service"

place_file Makefile.common "$COMMON_MAKEFILE"
place_file traefik.yml "$SERVICE_DIR/traefik.yml"
place_file compose.yaml "$SERVICE_DIR/compose.yaml"
place_file Makefile "$SERVICE_DIR/Makefile"

# ------------------------------------------------- 4. la validation à blanc

step "valider la configuration à blanc — make config"

if ! config_output=$("$MAKE" -C "$SERVICE_DIR" config 2>&1); then
    printf '%s\n' "$config_output" | tail -n 20
    fail "make config refuse la configuration du proxy : rien n'a été démarré"
fi

# ------------------------------------------------------------ 5. démarrer

step "démarrer le proxy — make up"

if ! up_output=$("$MAKE" -C "$SERVICE_DIR" up 2>&1); then
    printf '%s\n' "$up_output" | tail -n 20
    fail "make up n'a pas démarré le proxy"
fi
if [ "$running_before" -eq 1 ] && [ "$changed" -eq 0 ]; then
    step_unchanged "le proxy tourne déjà avec cette configuration"
else
    step_done "le proxy tourne"
fi

# ------------------------------------------------------------- 6. vérifier

step "vérifier par le chemin réel — un nom inconnu en HTTPS, puis le clair"

# 443 : le nom va dans le SNI et dans l'en-tête Host, la connexion va sur
# 127.0.0.1. Aucun routeur ne connaît ce nom : le proxy doit répondre 404, et
# pour cela présenter son certificat par défaut — d'où -k, ce n'est pas le
# certificat qu'on éprouve ici.
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
    fail "le proxy ne répond pas 404 à un nom inconnu sur $SECURE_PORT : il a répondu « ${secure_code:-rien} »"
fi

# 80 : la redirection est lue, jamais suivie — c'est elle qu'on éprouve, pas ce
# qu'il y a au bout.
clear_reply=$("$CURL" -sS -o /dev/null -w '%{http_code} %{redirect_url}' \
    --max-time "$CHECK_TIMEOUT_SECONDS" \
    "http://127.0.0.1:$CLEAR_PORT/" 2> /dev/null || true)
clear_code=${clear_reply%% *}
clear_target=${clear_reply#* }
info "port_${CLEAR_PORT}" "${clear_code:-sans réponse}"
info redirection "$clear_target"
if [ "$clear_code" != "301" ]; then
    fail "le clair ne redirige pas en 301 sur $CLEAR_PORT : il a répondu « ${clear_code:-rien} »"
fi
case $clear_target in
    https://*) ;;
    *) fail "la redirection du clair ne mène pas en HTTPS : « $clear_target »" ;;
esac

# La version, demandée au conteneur qui tourne — pas au tag de l'image.
traefik_version=$("$DOCKER" exec -- "$CONTAINER_NAME" traefik version 2> /dev/null | awk '/^Version:/ { print $2 }' || true)
info traefik "${traefik_version:-inconnue}"

# Le jeton de la zone n'est pas encore posé : Traefik sert son certificat par
# défaut, et aucun certificat ne sera demandé avant qu'il le soit.
if [ ! -f "$ACME_DIR/cloudflare.token" ]; then
    warn "le jeton DNS de la zone n'est pas encore sur cette machine : le proxy sert son certificat par défaut"
fi

# ------------------------------------------------------------------ 7. constat

if [ "$changed" -eq 1 ]; then
    done_changed
fi
done_unchanged
