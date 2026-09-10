#!/bin/bash
# Test de bout en bout d'une action, sur une machine jetable avec systemd — le
# runner de la CI ou le conteneur —, jamais sur un poste de travail : installer
# le paquet, amorcer la machine openCloud sur elle-même, lancer Diagnostiquer
# depuis l'interface, la suivre, relire journald, couper openCloud au milieu.
# usage : sudo packaging/test-action.sh <opencloud.deb> [--avec-socle]
#
# --avec-socle joue en plus « Poser le socle », qui ouvre le dépôt officiel de
# Docker et installe docker-ce, puis « Installer le proxy », qui prend les
# ports 80 et 443 de la machine, puis la zone Cloudflare et « Poser le jeton
# DNS », qui redémarre le proxy : à réserver à une machine jetable. Le job
# « package » de la CI joue ce script sur le runner GitHub lui-même, dont le
# Docker déjà posé et le témoin, enrôlé juste après, n'ont rien à y gagner.
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C DEBIAN_FRONTEND=noninteractive

DEB=$(readlink -f -- "$1")
WITH_SOCLE=${2:-}
# Poser le socle télécharge et déballe Docker : le délai des autres actions ne
# lui suffit pas.
SOCLE_TIMEOUT=600
# Installer le proxy tire l'image de Traefik puis attend qu'elle réponde.
PROXY_TIMEOUT=300
PROXY_SERVICE_DIR=/srv/workspace/system/traefik
# Un nom que rien ne route : c'est lui qui doit recevoir le 404 du proxy.
PROXY_UNKNOWN_NAME=proxy-inconnu.invalid
# Créer un hôte virtuel lit la machine, pose un fichier et attend que le proxy
# relise son dossier : rien de long.
VHOST_TIMEOUT=120
# Poser le jeton DNS redémarre le proxy et attend qu'il réponde.
DNS_TOKEN_TIMEOUT=180
# La zone du test : .test est réservé (RFC 2606), aucun DNS ne la sert. Les
# jetons sont fabriqués ici, pour un faux Cloudflare qui ne parle qu'à ce test.
ZONE_NAME=exemple.test
ZONE_TOKEN="jetable-$(date +%s)-zone"
ZONE_NEW_TOKEN="jetable-$(date +%s)-zone-tournee"
ZONE_TOKEN_FILE=/var/lib/opencloud/zones/$ZONE_NAME.token
PROXY_TOKEN_FILE=/srv/data/acme/cloudflare.token
# Le faux Cloudflare, construit par « make fake-cloudflare » dans dist/, que le
# conteneur voit sous /dist. Jamais installé, jamais publié.
FAKE_CLOUDFLARE=${FAKE_CLOUDFLARE:-/dist/fake-cloudflare}
FAKE_CLOUDFLARE_LISTEN=127.0.0.1:9123
# Le service témoin, versionné dans le dépôt, posé à la main comme le ferait un
# opérateur. .test est réservé (RFC 2606) : aucun DNS ne le sert.
TEMOIN_SOURCE=$(dirname -- "$0")/../examples/temoin
TEMOIN_ENVIRONMENT=prod
TEMOIN_SERVICE=temoin
TEMOIN_CONTAINER=$TEMOIN_ENVIRONMENT-$TEMOIN_SERVICE
TEMOIN_DIR=/srv/workspace/$TEMOIN_ENVIRONMENT/$TEMOIN_SERVICE
TEMOIN_NAME=temoin.example.test
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

if [ "$WITH_SOCLE" = "--avec-socle" ]; then
    step "Poser le socle, depuis l'interface, suivie jusqu'à sa conclusion"
    page_has /machines/local/actions/socle "/srv/workspace" || fail "l'écran « avant » du socle ne dit pas ce qu'il pose"
    page_has /machines/local/actions/socle "docker-ce" || fail "l'écran « avant » du socle ne liste pas les paquets"
    id=$(launch_action local socle)
    page=$(wait_for_conclusion "$id" "$SOCLE_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Poser le socle n'est pas « Appliquée »"; }
    printf '%s' "$page" | grep -q 'résultat: fait' || fail "la première pose ne dit pas « fait »"
    [ -d /srv/workspace ] || fail "/srv/workspace n'est pas posé"
    [ -d /srv/data ] || fail "/srv/data n'est pas posé"
    [ -f /var/lib/opencloud/socle.liste ] || fail "le fichier-garde du socle n'est pas écrit"
    grep -qx 'docker-ce' /var/lib/opencloud/socle.liste || fail "le fichier-garde ne nomme pas les paquets posés"

    step "la source officielle de Docker, posée avec sa clé"
    [ "$(stat -c '%U:%G %a' /etc/apt/keyrings/docker.asc)" = "root:root 644" ] || fail "la clé de Docker n'est pas root:root 0644"
    grep -q '^Signed-By: /etc/apt/keyrings/docker.asc$' /etc/apt/sources.list.d/docker.sources || fail "la source de Docker ne nomme pas sa clé"
    grep -q '^URIs: https://download.docker.com/linux/' /etc/apt/sources.list.d/docker.sources || fail "la source de Docker ne pointe pas sur son dépôt officiel"
    # Le paquet doit venir du dépôt de Docker, pas d'une reprise par la
    # distribution : c'était tout l'objet de la décision. Passer par un fichier
    # plutôt qu'un tube : grep -q sort au premier match, et pipefail ferait
    # échouer apt-cache sur le SIGPIPE.
    apt-cache policy docker-ce > "$WORK_DIR/docker-ce.policy"
    grep -q 'download.docker.com' "$WORK_DIR/docker-ce.policy" || fail "docker-ce ne vient pas du dépôt de Docker"
    dpkg-query -s docker-ce > /dev/null 2>&1 || fail "docker-ce n'est pas installé"
    dpkg-query -s docker-compose-plugin > /dev/null 2>&1 || fail "docker-compose-plugin n'est pas installé"
    dpkg-query -s jq > /dev/null 2>&1 || fail "jq n'est pas installé"
    printf '%s' "$page" | grep -q 'info: docker_compose=' || fail "le socle ne dit pas la version du plugin compose"
    if printf '%s' "$page" | grep -q 'info: docker_compose=absent'; then
        fail "le plugin compose n'a pas répondu"
    fi
    # Le démon Docker peut refuser de démarrer là où le conteneur n'a pas les
    # privilèges : le script le constate, il n'échoue pas pour autant.
    printf '%s' "$page" | grep -q 'info: docker_service=' || fail "le socle ne dit pas si le démon Docker tourne"

    step "le socle rejoué : inchangé"
    id=$(launch_action local socle)
    page=$(wait_for_conclusion "$id" "$SOCLE_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || fail "le second socle n'est pas « Appliquée »"
    printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "le second socle ne dit pas « inchangé »"

    step "Installer le proxy, depuis l'interface, suivie jusqu'à sa conclusion"
    page_has /machines/local/actions/proxy "$PROXY_SERVICE_DIR" || fail "l'écran « avant » du proxy ne dit pas où il pose le service"
    # L'écran montre le contenu rendu, pas seulement les noms de fichiers.
    page_has /machines/local/actions/proxy "read_only: true" || fail "l'écran « avant » du proxy ne montre pas les fichiers rendus"
    id=$(launch_action local proxy)
    page=$(wait_for_conclusion "$id" "$PROXY_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Installer le proxy n'est pas « Appliquée »"; }
    printf '%s' "$page" | grep -q 'résultat: fait' || fail "la première pose du proxy ne dit pas « fait »"
    [ -f "$PROXY_SERVICE_DIR/compose.yaml" ] || fail "le compose du proxy n'est pas posé"
    [ -f "$PROXY_SERVICE_DIR/traefik.yml" ] || fail "la configuration statique du proxy n'est pas posée"
    [ -f "$PROXY_SERVICE_DIR/Makefile" ] || fail "le Makefile du proxy n'est pas posé"
    [ -f /srv/workspace/Makefile.common ] || fail "les cibles standard ne sont pas posées"
    [ -d /srv/data/traefik ] || fail "le dossier des hôtes virtuels n'est pas posé"
    [ "$(stat -c '%U:%G %a' /srv/data/acme)" = "root:root 700" ] || fail "le dossier acme n'est pas root:root 0700"
    grep -q '@sha256:' "$PROXY_SERVICE_DIR/compose.yaml" || fail "l'image du proxy n'est pas épinglée par digest"

    step "le proxy répond : 404 en HTTPS à un nom inconnu, 301 en clair"
    code=$(curl -sS -k -o /dev/null -w '%{http_code}' --max-time 10 \
        --resolve "$PROXY_UNKNOWN_NAME:443:127.0.0.1" "https://$PROXY_UNKNOWN_NAME/")
    [ "$code" = "404" ] || fail "443 répond « $code » à un nom inconnu, attendu 404"
    # Lue, jamais suivie : c'est la redirection qu'on éprouve.
    reply=$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' --max-time 10 http://127.0.0.1:80/)
    case "$reply" in
        "301 https://"*) ;;
        *) fail "le clair ne redirige pas en 301 vers HTTPS : $reply" ;;
    esac
    printf '%s' "$page" | grep -q 'info: traefik=' || fail "le proxy ne dit pas la version de Traefik qui tourne"

    step "le proxy rejoué : inchangé"
    id=$(launch_action local proxy)
    page=$(wait_for_conclusion "$id" "$PROXY_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || fail "le second proxy n'est pas « Appliquée »"
    printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "le second proxy ne dit pas « inchangé »"
    docker network inspect proxy > /dev/null 2>&1 || fail "le réseau partagé « proxy » n'est pas posé"

    step "un faux Cloudflare, pour éprouver la zone sans compte ni jeton réels"
    [ -x "$FAKE_CLOUDFLARE" ] || fail "le faux Cloudflare n'est pas là : $FAKE_CLOUDFLARE — « make fake-cloudflare »"
    "$FAKE_CLOUDFLARE" -listen "$FAKE_CLOUDFLARE_LISTEN" -zone "$ZONE_NAME" \
        -tokens "$ZONE_TOKEN,$ZONE_NEW_TOKEN" > "$WORK_DIR/fake-cloudflare.log" 2>&1 &
    FAKE_CLOUDFLARE_PID=$!
    trap 'kill "$FAKE_CLOUDFLARE_PID" 2> /dev/null || true' EXIT
    # Sans jeton, le faux Cloudflare répond 401 : c'est une réponse, donc il
    # écoute. Pas de -f, qui ferait de ce 401 un échec.
    for _ in $(seq 1 20); do
        if curl -sS -o /dev/null "http://$FAKE_CLOUDFLARE_LISTEN/zones" 2> /dev/null; then break; fi
        sleep 1
    done
    # La clé est de développement : elle n'existe que pour ce test, et le
    # binaire n'accepte qu'une racine http:// ou https:// (internal/config).
    printf '\ncloudflare_api_url = "http://%s"\n' "$FAKE_CLOUDFLARE_LISTEN" >> /etc/opencloud/config.toml
    systemctl restart opencloud.service
    wait_for_health

    step "ajouter une zone : un jeton refusé ne laisse rien, un jeton valide s'enregistre"
    reply=$(post_zone /zones "zone=$ZONE_NAME" "token=jeton-qui-ne-vaut-rien")
    case "$reply" in 422*) ;; *) fail "un jeton refusé par Cloudflare doit refuser l'ajout : $reply" ;; esac
    [ ! -e "$ZONE_TOKEN_FILE" ] || fail "un jeton refusé a quand même été écrit"

    reply=$(post_zone /zones "zone=$ZONE_NAME" "token=$ZONE_TOKEN")
    case "$reply" in "303 $BASE/domains") ;; *) fail "l'ajout de la zone n'a pas abouti : $reply" ;; esac
    [ "$(stat -c '%U %a' "$ZONE_TOKEN_FILE")" = "opencloud 600" ] || fail "le jeton de la zone n'est pas opencloud 0600"
    page_has /domains "$ZONE_NAME" || fail "la vue Domaines ne liste pas la zone"
    if get_page /domains | grep -q "$ZONE_TOKEN"; then
        fail "la vue Domaines réaffiche le jeton"
    fi

    step "Poser le jeton DNS sur la machine locale, depuis l'interface"
    page_has "/machines/local/actions/dns-token?zone=$ZONE_NAME" "secret, non affiché" \
        || fail "l'écran « avant » ne dit pas que le fichier du jeton n'est pas affiché"
    if page_has "/machines/local/actions/dns-token?zone=$ZONE_NAME" "$ZONE_TOKEN"; then
        fail "l'écran « avant » montre le jeton"
    fi
    id=$(launch_dns_token "$ZONE_NAME")
    page=$(wait_for_conclusion "$id" "$DNS_TOKEN_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Poser le jeton DNS n'est pas « Appliquée »"; }
    printf '%s' "$page" | grep -q 'résultat: fait' || fail "la première pose ne dit pas « fait »"
    printf '%s' "$page" | grep -q 'info: jeton=' || fail "l'action ne dit pas l'empreinte du jeton posé"
    [ "$(stat -c '%U:%G %a' "$PROXY_TOKEN_FILE")" = "root:root 600" ] || fail "le jeton posé n'est pas root:root 0600"
    cmp -s "$ZONE_TOKEN_FILE" "$PROXY_TOKEN_FILE" || fail "le jeton posé n'est pas celui de la zone"

    step "le proxy est revenu après le redémarrage : 404 en HTTPS à un nom inconnu"
    code=$(curl -sS -k -o /dev/null -w '%{http_code}' --max-time 10 \
        --resolve "$PROXY_UNKNOWN_NAME:443:127.0.0.1" "https://$PROXY_UNKNOWN_NAME/")
    [ "$code" = "404" ] || fail "443 répond « $code » après la pose du jeton, attendu 404"

    step "le jeton ne traîne dans aucun journal"
    for unit in opencloud "oc-action-$id"; do
        journalctl -u "$unit" --no-pager > "$WORK_DIR/journal-$unit.log" 2>/dev/null || true
        if grep -q "$ZONE_TOKEN" "$WORK_DIR/journal-$unit.log"; then
            fail "le jeton apparaît dans le journal de $unit"
        fi
    done

    step "la vue Domaines dit que la machine est à jour"
    page_has /domains 'jeton à jour' || fail "la vue Domaines ne dit pas que la machine porte le jeton courant"

    step "le jeton rejoué : inchangé, et le proxy ne redémarre pas"
    id=$(launch_dns_token "$ZONE_NAME")
    page=$(wait_for_conclusion "$id" "$DNS_TOKEN_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || fail "la seconde pose n'est pas « Appliquée »"
    printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "la seconde pose ne dit pas « inchangé »"

    step "faire tourner le jeton : la machine porte encore l'ancien tant qu'on ne rejoue pas"
    reply=$(post_zone "/zones/$ZONE_NAME/token" "token=$ZONE_NEW_TOKEN")
    case "$reply" in "303 $BASE/domains") ;; *) fail "la rotation du jeton n'a pas abouti : $reply" ;; esac
    page_has /domains 'ancien jeton' || fail "la vue Domaines ne signale pas la machine restée sur l'ancien jeton"

    step "poser le nouveau jeton : « fait », et la vue redevient à jour"
    id=$(launch_dns_token "$ZONE_NAME")
    page=$(wait_for_conclusion "$id" "$DNS_TOKEN_TIMEOUT")
    printf '%s' "$page" | grep -q 'résultat: fait' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "la pose du nouveau jeton ne dit pas « fait »"; }
    cmp -s "$ZONE_TOKEN_FILE" "$PROXY_TOKEN_FILE" || fail "le jeton posé n'est pas le nouveau jeton de la zone"
    page_has /domains 'jeton à jour' || fail "la vue Domaines ne redit pas « à jour » après la pose du nouveau jeton"

    step "le service témoin, posé à la main sous la norme"
    # Tout ce qu'openCloud fait se fait aussi à la main : ce sont les trois
    # commandes du README du témoin, jouées telles quelles.
    mkdir -p "/srv/workspace/$TEMOIN_ENVIRONMENT"
    rm -rf "$TEMOIN_DIR"
    cp -r "$TEMOIN_SOURCE" "$TEMOIN_DIR"
    make -C "$TEMOIN_DIR" config > "$WORK_DIR/temoin-config.log" 2>&1 \
        || { cat "$WORK_DIR/temoin-config.log"; fail "make config du témoin"; }
    make -C "$TEMOIN_DIR" up > "$WORK_DIR/temoin-up.log" 2>&1 \
        || { cat "$WORK_DIR/temoin-up.log"; fail "make up du témoin"; }
    [ "$(docker container inspect --format '{{.State.Status}}' "$TEMOIN_CONTAINER")" = "running" ] \
        || fail "le conteneur du témoin ne tourne pas"

    step "Créer un hôte virtuel, depuis l'interface"
    page_has "/machines/local/actions/vhost" "Créer un hôte virtuel" || fail "l'écran « avant » de l'hôte virtuel n'existe pas"
    # Le port n'est pas un champ du formulaire : il est lu sur la machine.
    if page_has "/machines/local/actions/vhost" 'name="port"'; then
        fail "le formulaire demande un port : il doit être lu dans la définition du service"
    fi
    id=$(launch_vhost "$TEMOIN_NAME" "$TEMOIN_ENVIRONMENT" "$TEMOIN_SERVICE")
    page=$(wait_for_conclusion "$id" "$VHOST_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Créer un hôte virtuel n'est pas « Appliquée »"; }
    printf '%s' "$page" | grep -q 'résultat: fait' || fail "la première publication ne dit pas « fait »"
    printf '%s' "$page" | grep -q 'info: port=80' || fail "l'action ne dit pas le port qu'elle a lu"
    printf '%s' "$page" | grep -q 'info: dns=' || fail "l'action ne constate pas la résolution du nom"
    fragment=/srv/data/traefik/$TEMOIN_NAME.yml
    [ -f "$fragment" ] || fail "le fragment de $TEMOIN_NAME n'est pas posé"
    grep -q "http://$TEMOIN_CONTAINER:80" "$fragment" || fail "le fragment ne route pas vers le conteneur du témoin"
    if grep -q '@OC_PORT@' "$fragment"; then
        fail "le marqueur de port est resté dans le fragment"
    fi

    step "le nom répond : 200, et c'est bien le témoin au bout"
    code=$(curl -sS -k --max-time 10 --resolve "$TEMOIN_NAME:443:127.0.0.1" \
        -o "$WORK_DIR/temoin.html" -w '%{http_code}' "https://$TEMOIN_NAME/")
    [ "$code" = "200" ] || fail "$TEMOIN_NAME répond « $code », attendu 200"
    grep -q "Host: $TEMOIN_NAME" "$WORK_DIR/temoin.html" \
        || { cat "$WORK_DIR/temoin.html"; fail "la réponse ne vient pas du témoin"; }

    step "la vue Domaines liste le nom, avec le port constaté"
    page=$(get_page /domains)
    printf '%s' "$page" | grep -q "$TEMOIN_NAME" || fail "la vue Domaines ne liste pas $TEMOIN_NAME"
    printf '%s' "$page" | grep -q '>80<' || fail "la vue Domaines ne montre pas le port constaté"
    printf '%s' "$page" | grep -q "vhost-remove?domain=$TEMOIN_NAME" || fail "la vue Domaines n'offre pas de retirer le nom"

    step "l'hôte virtuel rejoué : inchangé"
    id=$(launch_vhost "$TEMOIN_NAME" "$TEMOIN_ENVIRONMENT" "$TEMOIN_SERVICE")
    page=$(wait_for_conclusion "$id" "$VHOST_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || fail "le second hôte virtuel n'est pas « Appliquée »"
    printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "le second hôte virtuel ne dit pas « inchangé »"

    step "Supprimer l'hôte virtuel : le nom répond 404 et la ligne disparaît"
    id=$(launch_vhost_removal "$TEMOIN_NAME")
    page=$(wait_for_conclusion "$id" "$VHOST_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || { printf '%s\n' "$page" | grep -o 'résultat:[^<]*' >&2 || true; fail "Supprimer un hôte virtuel n'est pas « Appliquée »"; }
    printf '%s' "$page" | grep -q 'résultat: fait' || fail "la suppression ne dit pas « fait »"
    [ ! -e "$fragment" ] || fail "le fragment de $TEMOIN_NAME est encore là"
    code=$(curl -sS -k -o /dev/null -w '%{http_code}' --max-time 10 \
        --resolve "$TEMOIN_NAME:443:127.0.0.1" "https://$TEMOIN_NAME/")
    [ "$code" = "404" ] || fail "$TEMOIN_NAME répond « $code » après suppression, attendu 404"
    if get_page /domains | grep -q "$TEMOIN_NAME"; then
        fail "la vue Domaines liste encore $TEMOIN_NAME"
    fi

    step "la suppression rejouée : inchangé"
    id=$(launch_vhost_removal "$TEMOIN_NAME")
    page=$(wait_for_conclusion "$id" "$VHOST_TIMEOUT")
    printf '%s' "$page" | grep -q 'Appliquée' || fail "la seconde suppression n'est pas « Appliquée »"
    printf '%s' "$page" | grep -q 'résultat: inchangé' || fail "la seconde suppression ne dit pas « inchangé »"
fi

step "openCloud coupé pendant une action : la reprise conclut sans intervention"
id=$(launch_diagnostiquer local)
systemctl restart opencloud.service
wait_for_health
page=$(wait_for_conclusion "$id")
printf '%s' "$page" | grep -q 'Appliquée' || fail "l'action lancée avant la coupure n'est pas « Appliquée » après la reprise"

step "l'historique de la machine montre les actions"
page_has /machines/local "$id" || fail "la fiche de la machine ne liste pas la dernière action"

printf '\nTout tient : amorçage, refus, lancement, suivi, journal, direct, reprise.\n'
if [ "$WITH_SOCLE" = "--avec-socle" ]; then
    printf 'Et le socle, le proxy, la zone et son jeton — posé, tourné, reposé —, le témoin\n'
    printf 'publié par son nom puis retiré : posés, éprouvés, puis inchangés.\n'
fi
