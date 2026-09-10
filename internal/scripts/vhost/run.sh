# shellcheck shell=bash
# Créer un hôte virtuel : publier un nom sur le proxy de cette machine, vers le
# conteneur d'un service posé dessus. Le fragment est rendu par Go et déposé
# sous files/ ; ce script le pose, il ne le compose pas. Sa seule écriture dans
# le fichier est le port, que Go ne peut pas connaître : il vit dans la
# définition du service, sur la machine. L'en-tête commun (lib.sh) est
# concaténé avant ce corps.
#
# La séquence est celle de 15-catalogue-actions.md §3 : refus si pas de proxy ;
# refus si le service n'a pas de définition ou pas de port ; comparer le
# fragment octet pour octet ; poser ; ne rien recharger ; vérifier en HTTPS sur
# 127.0.0.1 avec le nom en SNI et en Host.

FILES_DIR=$(cd -- "$(dirname -- "$0")" && pwd)/files
FRAGMENT_SOURCE="$FILES_DIR/fragment.yml"
# Le trou que Go laisse dans le fragment, et que ce script comble.
PORT_PLACEHOLDER='@OC_PORT@'

# Ce que le proxy occupe sur la machine (« Installer le proxy »).
PROXY_SERVICE_DIR=/srv/workspace/system/traefik
PROXY_CONTAINER_NAME=traefik
FRAGMENTS_DIR=/srv/data/traefik
SHARED_NETWORK=proxy
SECURE_PORT=443

WORKSPACE_DIR=/srv/workspace
FILE_MODE=0644
FILE_OWNER=root
FILE_GROUP=root

# Le proxy relit son dossier tout seul : quinze essais à une seconde lui
# laissent le temps sans tenir la machine (15-catalogue-actions.md §3).
CHECK_ATTEMPTS=15
CHECK_TIMEOUT_SECONDS=5

DOCKER=/usr/bin/docker
CURL=/usr/bin/curl
JQ=/usr/bin/jq

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

refuse_without_proxy() {
    refuse "cette machine ne tient aucun proxy : une route est servie par l'entrée et ne peut être publiée avant elle" \
        "lancer « Installer le proxy » sur cette machine, puis relancer l'action"
}

# L'état d'un conteneur, vide s'il n'existe pas.
container_status() {
    "$DOCKER" container inspect --format '{{.State.Status}}' -- "$1" 2> /dev/null || true
}

# Les trois questions posées à la définition rendue du service, sur le seul
# conteneur à publier. Guillemets simples : dans un programme jq, « $name » et
# « $network » sont des variables de jq, posées par --arg — le shell n'y touche
# pas, d'où le SC2016 désactivé.
# shellcheck disable=SC2016
PUBLISHED_NAME_QUERY='.services // {} | to_entries[] | select(.value.container_name == $name) | .key'
# shellcheck disable=SC2016
PUBLISHED_PORT_QUERY='.services // {} | to_entries[] | select(.value.container_name == $name) | (.value.labels // {})["opencloud.port"] // ""'
# shellcheck disable=SC2016
PUBLISHED_NETWORK_QUERY='.services // {} | to_entries[] | select(.value.container_name == $name) | ((.value.networks // {}) | has($network))'

ask_definition() {
    printf '%s' "$definition" \
        | "$JQ" -r --arg name "$CONTAINER_NAME" --arg network "$SHARED_NETWORK" "$1" \
        | head -n 1
}

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant d'écrire quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette action écrit dans la configuration du proxy, et elle ne tourne pas en root" \
        "relancer l'action depuis openCloud : le lanceur l'élève par systemd-run"
fi

require_binary "$DOCKER" "le service et le proxy tournent en conteneur"
require_binary "$CURL" "la vérification finale passe par une vraie requête"
require_binary "$JQ" "la définition du service se lit en JSON"

if [ ! -f "$FRAGMENT_SOURCE" ]; then
    fail "le fragment n'a pas été déposé avec l'action"
fi

# Le proxy : son dossier de service, son dossier de fragments, son conteneur.
# Les trois, parce que chacun manque pour une raison différente.
if [ ! -f "$PROXY_SERVICE_DIR/compose.yaml" ] || [ ! -d "$FRAGMENTS_DIR" ]; then
    refuse_without_proxy
fi
proxy_status=$(container_status "$PROXY_CONTAINER_NAME")
if [ "$proxy_status" != "running" ]; then
    refuse "le proxy de cette machine ne tourne pas : il est ${proxy_status:-absent}" \
        "relancer « Installer le proxy » sur cette machine, puis relancer l'action"
fi
if ! "$DOCKER" network inspect -- "$SHARED_NETWORK" > /dev/null 2>&1; then
    refuse "le réseau partagé « $SHARED_NETWORK » n'est pas sur cette machine : le proxy ne peut joindre aucun conteneur" \
        "relancer « Installer le proxy » sur cette machine, puis relancer l'action"
fi
info proxy "$proxy_status"

# --------------------------------------------------- 1. la définition du service

step "lire le port dans la définition du service"

# Les chemins sont dérivés des paramètres validés, jamais saisis
# (15-catalogue-actions.md §1).
SERVICE_DIR="$WORKSPACE_DIR/$OC_ENVIRONMENT/$OC_SERVICE"
SERVICE_COMPOSE="$SERVICE_DIR/compose.yaml"
CONTAINER_NAME="$OC_ENVIRONMENT-$OC_SERVICE"

if [ ! -d "$SERVICE_DIR" ]; then
    refuse "aucun service « $OC_ENVIRONMENT/$OC_SERVICE » n'est posé sur cette machine : $SERVICE_DIR est absent" \
        "déposer le service sous la norme, puis relancer l'action"
fi
if [ ! -f "$SERVICE_COMPOSE" ]; then
    refuse "le service « $OC_ENVIRONMENT/$OC_SERVICE » n'a pas de définition : $SERVICE_COMPOSE est absent" \
        "écrire le compose.yaml du service, puis relancer l'action"
fi

# La définition rendue, en JSON : c'est « make config » du service, avec un
# format de plus. Elle échoue si la définition ne tient pas debout, et c'est le
# refus qu'on veut, avant toute écriture. La sortie d'erreur part à part : un
# avertissement de compose mêlé au JSON le rendrait illisible.
config_log=$(mktemp)
if ! definition=$("$DOCKER" compose --project-directory "$SERVICE_DIR" -f "$SERVICE_COMPOSE" config --format json 2> "$config_log"); then
    tail -n 20 -- "$config_log"
    rm -f -- "$config_log"
    refuse "la définition du service « $OC_ENVIRONMENT/$OC_SERVICE » n'est pas valide : docker compose la refuse" \
        "corriger $SERVICE_COMPOSE — « make -C $SERVICE_DIR config » dit la même chose — puis relancer l'action"
fi
rm -f -- "$config_log"

# Le conteneur à publier est celui que le contrat nomme (03-modele.md) : le
# proxy le joint par ce nom-là sur le réseau partagé.
published=$(ask_definition "$PUBLISHED_NAME_QUERY")
if [ -z "$published" ]; then
    refuse "la définition de « $OC_ENVIRONMENT/$OC_SERVICE » ne nomme aucun conteneur « $CONTAINER_NAME »" \
        "poser « container_name: $CONTAINER_NAME » sur le conteneur à publier, puis relancer l'action"
fi
info conteneur "$CONTAINER_NAME"

port=$(ask_definition "$PUBLISHED_PORT_QUERY")
if [ -z "$port" ]; then
    refuse "le conteneur « $CONTAINER_NAME » ne déclare pas le port à publier" \
        "poser le label « opencloud.port=<port> » sur ce conteneur, puis relancer l'action"
fi
# Borné avant d'entrer dans le fragment : c'est la seule valeur que ce script
# écrit dans un fichier, et elle vient de la machine.
if [[ ! $port =~ ^[0-9]{1,5}$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    refuse "le conteneur « $CONTAINER_NAME » déclare « opencloud.port=$port », qui n'est pas un port" \
        "corriger le label opencloud.port du conteneur, puis relancer l'action"
fi
info port "$port"

on_shared_network=$(ask_definition "$PUBLISHED_NETWORK_QUERY")
if [ "$on_shared_network" != "true" ]; then
    refuse "le conteneur « $CONTAINER_NAME » ne rejoint pas le réseau partagé « $SHARED_NETWORK » : le proxy ne peut pas le joindre" \
        "ajouter « networks: [$SHARED_NETWORK] » au conteneur et le réseau externe à la définition, puis relancer l'action"
fi

# Un fragment vers un conteneur arrêté publierait un nom qui ne répond pas :
# c'est un refus avant écriture, pas un échec après.
service_status=$(container_status "$CONTAINER_NAME")
if [ "$service_status" != "running" ]; then
    refuse "le conteneur « $CONTAINER_NAME » ne tourne pas : il est ${service_status:-absent}" \
        "démarrer le service — make -C $SERVICE_DIR up — puis relancer l'action"
fi

# ------------------------------------------------------------- 2. le fragment

step "poser le fragment de $OC_DOMAIN"

FRAGMENT_TARGET="$FRAGMENTS_DIR/$OC_DOMAIN.yml"

# Le port remplace son marqueur, et rien d'autre ne bouge. Substitution de
# bash sur une valeur bornée à cinq chiffres : ni eval, ni sed, ni printf de
# format.
template=$(cat -- "$FRAGMENT_SOURCE")
fragment=${template//"$PORT_PLACEHOLDER"/$port}
if [ "$fragment" = "$template" ]; then
    fail "le fragment déposé ne porte pas le marqueur de port : il n'a pas été rendu par openCloud"
fi

# Le temporaire naît dans le dossier de destination : c'est ce qui rend le
# rename atomique. umask est à 077 dans l'en-tête commun, d'où le chmod.
rendered="$FRAGMENT_TARGET.opencloud-tmp"
printf '%s\n' "$fragment" > "$rendered"
chown -- "$FILE_OWNER:$FILE_GROUP" "$rendered"
chmod -- "$FILE_MODE" "$rendered"

if [ -f "$FRAGMENT_TARGET" ] && cmp -s -- "$rendered" "$FRAGMENT_TARGET"; then
    rm -f -- "$rendered"
    step_unchanged "$FRAGMENT_TARGET"
else
    # Traefik relit ce dossier quand il veut : il ne doit jamais y voir un
    # fichier à moitié écrit. Rien à recharger ensuite.
    mv -f -- "$rendered" "$FRAGMENT_TARGET"
    step_done "$FRAGMENT_TARGET écrit"
fi

# ------------------------------------------------------------- 3. vérifier

step "vérifier par le chemin réel — le nom en SNI et en Host, sur 127.0.0.1"

# -k : c'est la route qu'on éprouve, pas le certificat — il n'y en a pas
# encore, le proxy sert son certificat par défaut.
code=""
for _ in $(seq 1 "$CHECK_ATTEMPTS"); do
    code=$("$CURL" -sS -k -o /dev/null -w '%{http_code}' \
        --max-time "$CHECK_TIMEOUT_SECONDS" \
        --resolve "$OC_DOMAIN:$SECURE_PORT:127.0.0.1" \
        "https://$OC_DOMAIN/" 2> /dev/null || true)
    # 404 : aucun routeur ne connaît le nom. 502, 503, 504 : le routeur est là,
    # le conteneur ne répond pas. Tout le reste vient du service.
    case $code in
        "" | 404 | 502 | 503 | 504) ;;
        *) break ;;
    esac
    sleep 1
done
info code "${code:-sans réponse}"
case $code in
    "" | 404)
        fail "le proxy ne route pas $OC_DOMAIN : il répond « ${code:-rien} »"
        ;;
    502 | 503 | 504)
        fail "le proxy route $OC_DOMAIN mais $CONTAINER_NAME ne répond pas : « $code »"
        ;;
esac

# ---------------------------------------------------------- 4. la résolution

step "constater la résolution du nom"

# Un constat, jamais un refus : le nom peut être publié avant d'être résolu.
resolved=$(getent hosts -- "$OC_DOMAIN" 2> /dev/null | awk 'NR == 1 { print $1 }' || true)
info dns "${resolved:-absent}"

# Les adresses de la machine, telles que le noyau les donne à hostname.
local_addresses=$(hostname -I 2> /dev/null || true)
if [ -z "$resolved" ]; then
    warn "$OC_DOMAIN ne résout vers aucune adresse : « Créer l'enregistrement DNS » posera le A ou le CNAME"
elif [ -z "$local_addresses" ]; then
    warn "les adresses de cette machine n'ont pas pu être lues : la résolution de $OC_DOMAIN n'a pas été comparée"
elif [[ " $local_addresses " != *" $resolved "* ]]; then
    warn "$OC_DOMAIN résout vers $resolved, qui n'est pas une adresse de cette machine : « Créer l'enregistrement DNS » corrigera la zone"
fi

# ------------------------------------------------------------------ 5. constat

if [ "$changed" -eq 1 ]; then
    done_changed
fi
done_unchanged
