#!/bin/bash
# Ce que les tests de bout en bout partagent : les étapes, l'échec qui montre
# le journal, la session HTTP avec son jeton CSRF, le lancement et l'attente
# d'une action. Sourcé par test-action.sh et test-enroll.sh, jamais joué seul.
# Attend BASE, VERSION, WORK_DIR et COOKIES posés par l'appelant.

step() { printf '\n== %s\n' "$*"; }

fail() {
    printf 'ÉCHEC : %s\n' "$*" >&2
    journalctl -u opencloud --no-pager 2>/dev/null | tail -n 40 >&2 || true
    journalctl -u 'oc-action-*' --no-pager 2>/dev/null | tail -n 40 >&2 || true
    exit 1
}

wait_for_health() {
    for _ in $(seq 1 30); do
        if curl -fsS "$BASE/healthz" 2>/dev/null | grep -q "\"version\":\"$VERSION\""; then
            return 0
        fi
        sleep 1
    done
    fail "l'interface ne répond pas en version $VERSION"
}

# Le jeton CSRF de la page demandée, avec la session du bocal de cookies.
csrf_of() {
    curl -fsS -c "$COOKIES" -b "$COOKIES" "$BASE$1" \
        | sed -n 's/.*name="_csrf" value="\([^"]*\)".*/\1/p' | head -n 1
}

get_page() {
    curl -fsS -c "$COOKIES" -b "$COOKIES" "$BASE$1"
}

# La page contient-elle ce texte ? Jamais « curl | grep -q » : grep s'arrête
# au premier résultat, curl reçoit un SIGPIPE et, sous pipefail, le pipeline
# échoue alors que le texte était là. On lit la page en entier, puis on cherche.
page_has() {
    local page
    page=$(get_page "$1")
    printf '%s' "$page" | grep -q -- "$2"
}

# POST d'un formulaire ; imprime le code HTTP et l'URL de redirection.
post_form() {
    local path=$1
    shift
    curl -sS -c "$COOKIES" -b "$COOKIES" -o /dev/null -w '%{http_code} %{redirect_url}\n' \
        -X POST "$BASE$path" "$@"
}

# Se connecte avec le compte par défaut et pose le mot de passe de test : en
# production, le mot de passe par défaut n'est pas admis et chaque page renvoie
# vers /password tant qu'il n'est pas changé.
login_and_set_password() {
    local token reply
    token=$(csrf_of /login)
    [ -n "$token" ] || fail "pas de jeton CSRF sur la page de connexion"
    reply=$(post_form /login --data-urlencode "_csrf=$token" --data-urlencode "username=admin" --data-urlencode "password=opencloud")
    case "$reply" in 303*) ;; *) fail "la connexion a échoué : $reply" ;; esac
    curl -sS -o /dev/null -w '%{redirect_url}\n' -c "$COOKIES" -b "$COOKIES" "$BASE/machines/local" \
        | grep -q '/password$' || fail "le mot de passe par défaut n'a pas forcé le changement"
    token=$(csrf_of /password)
    reply=$(post_form /password --data-urlencode "_csrf=$token" --data-urlencode "current_password=opencloud" \
        --data-urlencode "new_password=$PASSWORD" --data-urlencode "confirm_password=$PASSWORD")
    case "$reply" in 303*) ;; *) fail "le changement de mot de passe a échoué : $reply" ;; esac
}

# Lance une action sur une machine depuis l'interface, et imprime son
# identifiant. usage : launch_action <machine> <action>
launch_action() {
    local machine=$1 kind=$2 token reply
    token=$(csrf_of "/machines/$machine/actions/$kind")
    [ -n "$token" ] || fail "pas de jeton CSRF sur l'écran « avant » de $kind sur $machine"
    reply=$(post_form "/machines/$machine/actions/$kind" --data-urlencode "_csrf=$token")
    case "$reply" in
        "303 $BASE/actions/"*) printf '%s\n' "${reply#303 "$BASE"/actions/}" ;;
        *) fail "le lancement de $kind n'a pas redirigé vers l'action : $reply" ;;
    esac
}

launch_diagnostiquer() {
    launch_action "$1" diagnostiquer
}

# Lance une action avec ses paramètres, sur la machine openCloud. Le jeton CSRF
# vient de l'écran « avant » de l'action, comme pour un navigateur.
# usage : launch_action_with <action> <champ=valeur>…
launch_action_with() {
    local kind=$1 token reply
    shift
    token=$(csrf_of "/machines/local/actions/$kind")
    [ -n "$token" ] || fail "pas de jeton CSRF sur l'écran « avant » de $kind"

    local fields=(--data-urlencode "_csrf=$token")
    local field
    for field in "$@"; do
        fields+=(--data-urlencode "$field")
    done

    reply=$(post_form "/machines/local/actions/$kind" "${fields[@]}")
    case "$reply" in
        "303 $BASE/actions/"*) printf '%s\n' "${reply#303 "$BASE"/actions/}" ;;
        *) fail "le lancement de $kind n'a pas redirigé vers l'action : $reply" ;;
    esac
}

# usage : launch_vhost <domaine> <environnement> <service>
launch_vhost() {
    launch_action_with vhost "domain=$1" "environment=$2" "service=$3"
}

# La suppression coupe le nom : l'écran « avant » exige la confirmation, et le
# POST doit la porter.
launch_vhost_removal() {
    launch_action_with vhost-remove "domain=$1" "confirm=oui"
}

# Poser le jeton DNS coupe brièvement — Traefik redémarre si le fichier change
# —, donc l'écran « avant » exige la confirmation.
# usage : launch_dns_token <zone>
launch_dns_token() {
    launch_action_with dns-token "zone=$1" "confirm=oui"
}

# POST d'un formulaire de zone. Le jeton part dans le corps, jamais dans l'URL.
# usage : post_zone <chemin> <champ=valeur>…
post_zone() {
    local path=$1 token
    shift
    token=$(csrf_of /domains)
    [ -n "$token" ] || fail "pas de jeton CSRF sur la vue Domaines"

    local fields=(--data-urlencode "_csrf=$token")
    local field
    for field in "$@"; do
        fields+=(--data-urlencode "$field")
    done
    post_form "$path" "${fields[@]}"
}

# Attend qu'une action soit conclue et imprime sa page. Le second argument dit
# combien de secondes attendre : Poser le socle télécharge des paquets, elle ne
# tient pas dans le délai des autres.
wait_for_conclusion() {
    local id=$1 page
    for _ in $(seq 1 "${2:-90}"); do
        page=$(get_page "/actions/$id")
        if printf '%s' "$page" | grep -q -e 'Appliquée' -e 'Échouée' -e 'Refusée'; then
            printf '%s' "$page"
            return 0
        fi
        sleep 1
    done
    fail "l'action $id n'a pas conclu en ${2:-90} s"
}
