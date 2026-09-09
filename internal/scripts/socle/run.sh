# shellcheck shell=bash
# Poser le socle : les répertoires de la norme /srv, la source apt officielle
# de Docker, puis les paquets de la liste versionnée du dépôt. Go insère cette
# liste et la clé de Docker devant ce corps, dans les variables PACKAGES et
# DOCKER_KEY (internal/scripts/socle/packages.txt et docker.asc) — ni l'une ni
# l'autre ne passe par une ligne de commande, et la clé n'est pas téléchargée
# ici : elle est versionnée dans le dépôt et son empreinte est vérifiée par un
# test Go. L'en-tête commun (lib.sh) est concaténé encore avant. Le script
# tourne en root : oc-launch le lance par systemd-run.

# Go pose PACKAGES et DOCKER_KEY au-dessus ; ces deux lignes ne font que garder
# le fichier lisible seul, shellcheck compris.
PACKAGES=${PACKAGES:-}
DOCKER_KEY=${DOCKER_KEY:-}

WORKSPACE_DIR=/srv/workspace
DATA_DIR=/srv/data
# La norme ne fixe ni propriétaire ni mode (04-implantation.md) : ceux que le
# FHS donne à /srv lui-même. Rien sous /srv n'appartient à openCloud, donc un
# répertoire déjà là n'est pas retouché.
DIR_MODE=0755
DIR_OWNER=root
DIR_GROUP=root

# L'état d'openCloud vit dans /var/lib/opencloud, jamais sous /srv
# (04-implantation.md). Le fichier-garde y tient la liste posée, nom par nom :
# une machine posée tôt garderait sinon l'ancienne surface en silence
# (18-reprise-code-your-cloud.md).
GUARD_DIR=/var/lib/opencloud
GUARD_FILE=/var/lib/opencloud/socle.liste
GUARD_MODE=0644

# Le dépôt officiel de Docker, posé sur chaque machine : ni Debian 12 ni
# Ubuntu 24.04 ne portent le plugin compose, et docker.io n'est pas utilisé.
# Emplacement de la clé, forme de la source et noms des paquets viennent de
# docs.docker.com/engine/install/debian/ et /engine/install/ubuntu/.
DOCKER_REPOSITORY_BASE=https://download.docker.com/linux
KEYRINGS_DIR=/etc/apt/keyrings
KEYRINGS_DIR_MODE=0755
DOCKER_KEY_FILE=/etc/apt/keyrings/docker.asc
DOCKER_KEY_MODE=0644
DOCKER_SOURCES_FILE=/etc/apt/sources.list.d/docker.sources
DOCKER_SOURCES_MODE=0644
DOCKER_COMPONENTS=stable
OS_RELEASE=/etc/os-release
# Les noms de code que Docker publie, distribution par distribution (mêmes
# pages). Un système absent de ces listes est un refus, pas une source apt qui
# renverra des 404 à chaque apt-get update.
DEBIAN_SUITES=(bookworm trixie)
UBUNTU_SUITES=(jammy noble resolute)
# Ce que la doc de Docker nomme « Uninstall old versions » : ces paquets se
# disputent le dépôt officiel. openCloud n'est pas propriétaire de la machine,
# il ne retire pas ce qu'il n'a pas posé — il refuse et donne le geste.
CONFLICTING_PACKAGES=(docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc)

APT_GET=/usr/bin/apt-get
APT_CACHE=/usr/bin/apt-cache
DPKG=/usr/bin/dpkg
DPKG_QUERY=/usr/bin/dpkg-query
APT_LISTS_DIR=/var/lib/apt/lists
# Au-delà d'un jour, les listes de paquets se rafraîchissent avant d'installer.
APT_LISTS_MAX_AGE_DAYS=1
# apt n'attend pas le verrou : un verrou tenu est un refus nommé, pas une
# attente sans fin.
APT_LOCK_OPTION=DPkg::Lock::Timeout=0
# Ce que docker-ce, containerd.io et le plugin compose déballés demandent, avec
# de la marge.
MIN_FREE_MIB=1024

export DEBIAN_FRONTEND=noninteractive

# set -e sort sans rien dire, et le contrat veut une ligne de constat. set -E
# porte le piège dans les fonctions, où la moitié des commandes vivent.
set -E
trap 'fail "une commande de la séquence a échoué, ligne $LINENO"' ERR

# Ce que la machine a bougé : « inchangé » n'est vrai que si rien n'a changé.
changed=0

# La source apt de Docker vient d'être posée ou corrigée : apt ne connaît pas
# encore ses paquets, et un apt-get update devient obligatoire, listes fraîches
# ou non.
docker_sources_changed=0

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
            "installer le paquet qui fournit $path, puis relancer l'action"
    fi
}

# L'espace libre d'un point de montage, en mébioctets.
free_space_mib() {
    local path=$1 blocks
    blocks=$(df -P -- "$path" | awk 'NR == 2 { print $4 }')
    printf '%s' "$((blocks / 1024))"
}

# Qui tient apt, s'il est tenu. Lu dans /proc : ni fuser ni lsof ne sont
# garantis sur une machine neuve, et un verrou apt se voit à son processus.
apt_holder() {
    local entry pid command
    for entry in /proc/[0-9]*; do
        pid=${entry#/proc/}
        command=$(cat -- "$entry/comm" 2> /dev/null) || continue
        case $command in
            apt | apt-get | dpkg | unattended-upgr | aptitude)
                if [ "$pid" != "$$" ]; then
                    printf '%s (%s)' "$pid" "$command"
                    return 0
                fi
                ;;
        esac
    done
    return 1
}

refuse_apt_lock() {
    refuse "un autre processus tient le verrou d'apt : $1" \
        "attendre qu'il finisse — c'est souvent unattended-upgrades, quelques minutes — puis relancer l'action"
}

# La première ligne d'erreur d'apt, sur une seule ligne : c'est elle qui nomme
# le processus fautif.
apt_error_line() {
    printf '%s' "$1" | sed -n 's/^E: //p' | head -n 1
}

# dpkg dit ce qui est là, pas apt. « installed » seul compte : un paquet à
# demi configuré n'est pas installé.
package_installed() {
    local status
    status=$("$DPKG_QUERY" -s -- "$1" 2> /dev/null | sed -n 's/^Status: //p') || return 1
    [ "$status" = "install ok installed" ]
}

# Un paquet que les dépôts de la machine ne portent pas est un refus qui le
# nomme : c'est la liste versionnée qu'on corrige, pas la machine.
require_candidate() {
    local package=$1 candidate
    candidate=$("$APT_CACHE" policy -- "$package" | awk -F ': ' '/^  Candidate:/ { print $2 }')
    if [ -z "$candidate" ] || [ "$candidate" = "(none)" ]; then
        refuse "le paquet « $package » n'existe dans aucun dépôt de cette machine" \
            "vérifier les dépôts apt de la machine, ou retirer « $package » de la liste du socle"
    fi
}

# Une valeur de /etc/os-release, lue et non sourcée : ce fichier est du shell,
# et le sourcer exécuterait ce qu'il contient. La clé est toujours une
# constante de ce script.
os_release_value() {
    local key=$1 line
    line=$(grep -m 1 -- "^$key=" "$OS_RELEASE" 2> /dev/null) || return 1
    line=${line#"$key="}
    # os-release autorise les guillemets autour de la valeur.
    line=${line#\"}
    printf '%s' "${line%\"}"
}

# Vrai si le fichier porte déjà exactement ce contenu : rien à écrire, donc
# rien à annoncer comme un changement.
file_has_content() {
    local path=$1 content=$2
    [ -f "$path" ] && printf '%s' "$content" | cmp -s - "$path"
}

# Écriture atomique : le temporaire naît dans le même dossier, puis un rename.
# apt lit ces fichiers quand il veut ; il ne doit jamais en voir un à moitié
# écrit. umask 077 crée le temporaire fermé, chmod l'ouvre ensuite.
write_file_atomically() {
    local path=$1 mode=$2 content=$3
    local candidate="$path.opencloud-tmp"
    printf '%s' "$content" > "$candidate"
    chmod "$mode" -- "$candidate"
    chown "$DIR_OWNER:$DIR_GROUP" -- "$candidate"
    mv -f -- "$candidate" "$path"
}

# La norme prime, mais openCloud ne possède pas /srv : il crée ce qui manque et
# ne touche à rien d'autre (04-implantation.md).
ensure_directory() {
    local path=$1 key=$2
    if [ -e "$path" ] && [ ! -d "$path" ]; then
        refuse "$path existe et n'est pas un répertoire : la norme d'implantation en attend un" \
            "déplacer ou renommer $path, puis relancer l'action"
    fi
    if [ -d "$path" ]; then
        info "$key" "$(stat -c '%U:%G %a' -- "$path")"
        step_unchanged "$path"
        return 0
    fi
    install -d -o "$DIR_OWNER" -g "$DIR_GROUP" -m "$DIR_MODE" -- "$path"
    info "$key" "$(stat -c '%U:%G %a' -- "$path")"
    step_done "$path posé en $DIR_MODE, $DIR_OWNER:$DIR_GROUP"
}

# Rafraîchir les listes n'est pas un changement de la machine : c'est ce qu'apt
# sait du monde, pas ce qui est posé dessus.
refresh_lists() {
    local update_output
    if ! update_output=$("$APT_GET" -o "$APT_LOCK_OPTION" update 2>&1); then
        case $update_output in
            *"Could not get lock"*) refuse_apt_lock "$(apt_error_line "$update_output")" ;;
        esac
        printf '%s\n' "$update_output" | tail -n 20
        fail "apt-get update a échoué"
    fi
    step "listes de paquets rafraîchies"
}

# Les listes de paquets périmées feraient installer une version que le dépôt ne
# porte plus.
refresh_lists_if_stale() {
    local recent
    # C'est le dossier qui date la dernière moisson : les fichiers, eux,
    # gardent la date du serveur, toujours plus vieille qu'elle.
    recent=$(find "$APT_LISTS_DIR" -maxdepth 0 -mtime "-$APT_LISTS_MAX_AGE_DAYS" -print 2> /dev/null || true)
    if [ -n "$recent" ]; then
        step "listes de paquets déjà à jour"
        return 0
    fi
    refresh_lists
}

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant d'écrire quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette action installe des paquets et crée des répertoires système, et elle ne tourne pas en root" \
        "relancer l'action depuis openCloud : le lanceur l'élève par systemd-run"
fi
if [ ! -d /run/systemd/system ]; then
    refuse "cette machine ne tourne pas sous systemd" \
        "poser le socle sur une machine gérée par systemd"
fi
if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
    refuse "cette machine n'expose pas cgroup v2 : Docker et systemd-run en dépendent" \
        "démarrer le noyau avec systemd.unified_cgroup_hierarchy=1, puis relancer l'action"
fi

# Un gestionnaire concurrent porte déjà le proxy, le pare-feu et les
# certificats : openCloud ne cohabite pas avec lui (15-catalogue-actions.md §5).
if [ -d /usr/local/psa ]; then
    refuse "cette machine est déjà tenue par Plesk : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "poser le socle sur une machine qu'openCloud est seul à gérer"
fi
if [ -d /usr/local/cpanel ]; then
    refuse "cette machine est déjà tenue par cPanel : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "poser le socle sur une machine qu'openCloud est seul à gérer"
fi
if systemctl is-active --quiet certbot.timer 2> /dev/null; then
    refuse "un certbot est actif sur cette machine : deux clients ACME se disputeraient le renouvellement du même domaine" \
        "désactiver certbot.timer, ou poser le socle sur une machine qu'openCloud est seul à gérer"
fi

require_binary "$APT_GET" "apt-get installe les paquets du socle"
require_binary "$APT_CACHE" "apt-cache dit si un paquet existe dans les dépôts"
require_binary "$DPKG" "dpkg donne l'architecture que la source de Docker déclare"
require_binary "$DPKG_QUERY" "dpkg-query dit ce qui est déjà installé"
require_binary /usr/bin/install "install pose les répertoires de la norme"

if [ ! -d "$GUARD_DIR" ]; then
    refuse "$GUARD_DIR est absent : cette machine n'a pas été enrôlée" \
        "enrôler la machine, puis relancer l'action"
fi

free_mib=$(free_space_mib /var)
if [ "$free_mib" -lt "$MIN_FREE_MIB" ]; then
    refuse "cette machine n'a que $free_mib Mio libres sur /var, et poser le socle en demande $MIN_FREE_MIB" \
        "faire de la place sur /var — apt-get clean, journalctl --vacuum-size — puis relancer l'action"
fi
info espace_libre_var "$free_mib Mio"

if holder=$(apt_holder); then
    refuse_apt_lock "processus $holder"
fi

if [ -z "$PACKAGES" ]; then
    fail "la liste de paquets du socle est vide : le script a été assemblé sans elle"
fi
mapfile -t wanted <<< "$PACKAGES"
info paquets_demandes "${wanted[*]}"

if [ -z "$DOCKER_KEY" ]; then
    fail "la clé du dépôt de Docker est vide : le script a été assemblé sans elle"
fi

# La source de Docker ne se pose que là où Docker publie. Ailleurs, apt
# répondrait 404 à chaque mise à jour, et le socle aurait cassé la machine.
distribution=$(os_release_value ID) || distribution=""
codename=$(os_release_value VERSION_CODENAME) || codename=""
case $distribution in
    debian) suites=("${DEBIAN_SUITES[@]}") ;;
    ubuntu) suites=("${UBUNTU_SUITES[@]}") ;;
    *)
        refuse "cette machine se déclare « ${distribution:-inconnue} » dans $OS_RELEASE, et le dépôt de Docker ne sert que Debian et Ubuntu" \
            "poser le socle sur une machine Debian ou Ubuntu"
        ;;
esac

suite_is_served=0
for suite in "${suites[@]}"; do
    if [ "$suite" = "$codename" ]; then
        suite_is_served=1
    fi
done
if [ "$suite_is_served" -ne 1 ]; then
    refuse "le dépôt de Docker ne publie rien pour « ${codename:-inconnu} » : sur $distribution il sert ${suites[*]}" \
        "poser le socle sur une version que Docker sert — ${suites[*]} — ou attendre qu'il publie pour « ${codename:-inconnu} »"
fi
architecture=$("$DPKG" --print-architecture)
info systeme "$distribution $codename $architecture"

# Les paquets Docker de la distribution se disputent ceux du dépôt officiel.
# openCloud ne retire pas ce qu'il n'a pas posé : il nomme et rend la main.
conflicting=()
for package in "${CONFLICTING_PACKAGES[@]}"; do
    if package_installed "$package"; then
        conflicting+=("$package")
    fi
done
if [ "${#conflicting[@]}" -ne 0 ]; then
    refuse "cette machine porte déjà ${conflicting[*]}, que le dépôt officiel de Docker remplace : openCloud n'a pas posé ces paquets, il ne les retire pas" \
        "retirer ces paquets — apt-get remove ${conflicting[*]} — puis relancer l'action"
fi

# Une horloge en dérive fait rejeter les signatures des dépôts. C'est un
# constat, pas un refus : apt le dira lui-même, et nommément.
synchronized=$(timedatectl show -p NTPSynchronized --value 2> /dev/null || true)
info horloge_synchronisee "${synchronized:-inconnu}"

# ------------------------------------------------- 1. les répertoires de /srv

step "les répertoires de la norme /srv"

ensure_directory "$WORKSPACE_DIR" norme_workspace
ensure_directory "$DATA_DIR" norme_data

# --------------------------------------------------- 2. la source de Docker

step "la source apt officielle de Docker"

# install -d ne retouche pas un dossier existant : le mode n'est posé qu'à la
# création, et /etc/apt/keyrings appartient souvent déjà à la distribution.
if [ -d "$KEYRINGS_DIR" ]; then
    step_unchanged "$KEYRINGS_DIR"
else
    install -d -o "$DIR_OWNER" -g "$DIR_GROUP" -m "$KEYRINGS_DIR_MODE" -- "$KEYRINGS_DIR"
    step_done "$KEYRINGS_DIR posé en $KEYRINGS_DIR_MODE"
fi

# La clé est versionnée dans le dépôt d'openCloud et son empreinte est vérifiée
# par un test Go : rien n'est téléchargé ici, et une clé remplacée par erreur se
# voit avant d'atteindre une machine.
if file_has_content "$DOCKER_KEY_FILE" "$DOCKER_KEY"; then
    step_unchanged "clé $DOCKER_KEY_FILE"
else
    write_file_atomically "$DOCKER_KEY_FILE" "$DOCKER_KEY_MODE" "$DOCKER_KEY"
    docker_sources_changed=1
    step_done "clé $DOCKER_KEY_FILE écrite en $DOCKER_KEY_MODE"
fi
info cle_docker "$(stat -c '%U:%G %a' -- "$DOCKER_KEY_FILE")"

# Format deb822 : Signed-By nomme la clé, et cette source-là seule est signée
# par elle (docs.docker.com/engine/install/debian/).
docker_sources_content="Types: deb
URIs: $DOCKER_REPOSITORY_BASE/$distribution
Suites: $codename
Components: $DOCKER_COMPONENTS
Architectures: $architecture
Signed-By: $DOCKER_KEY_FILE
"
if file_has_content "$DOCKER_SOURCES_FILE" "$docker_sources_content"; then
    step_unchanged "source $DOCKER_SOURCES_FILE"
else
    write_file_atomically "$DOCKER_SOURCES_FILE" "$DOCKER_SOURCES_MODE" "$docker_sources_content"
    docker_sources_changed=1
    step_done "source $DOCKER_SOURCES_FILE écrite : $DOCKER_REPOSITORY_BASE/$distribution $codename $DOCKER_COMPONENTS"
fi
info source_docker "$DOCKER_REPOSITORY_BASE/$distribution $codename $DOCKER_COMPONENTS"

# La source vient de changer : les listes d'hier, si fraîches soient-elles, ne
# portent pas encore ses paquets.
if [ "$docker_sources_changed" -eq 1 ]; then
    refresh_lists
fi

# ----------------------------------------------------------- 3. les paquets

step "les paquets de la liste versionnée"

missing=()
for package in "${wanted[@]}"; do
    if ! package_installed "$package"; then
        missing+=("$package")
    fi
done

if [ "${#missing[@]}" -eq 0 ]; then
    info paquets_manquants aucun
    step_unchanged "les ${#wanted[@]} paquets de la liste"
else
    info paquets_manquants "${missing[*]}"
    refresh_lists_if_stale
    for package in "${missing[@]}"; do
        require_candidate "$package"
    done

    step "installation de ${#missing[@]} paquet(s) : ${missing[*]}"
    if ! install_output=$("$APT_GET" -o "$APT_LOCK_OPTION" install -y --no-install-recommends -- "${missing[@]}" 2>&1); then
        case $install_output in
            *"Could not get lock"*) refuse_apt_lock "$(apt_error_line "$install_output")" ;;
        esac
        printf '%s\n' "$install_output" | tail -n 30
        fail "apt-get install a échoué pour : ${missing[*]}"
    fi

    # Vérifier par le chemin réel : c'est dpkg qui dit ce qui est posé, jamais
    # la sortie d'apt.
    for package in "${missing[@]}"; do
        if ! package_installed "$package"; then
            fail "le paquet « $package » n'est pas installé alors qu'apt-get install a réussi"
        fi
    done
    step_done "paquets installés : ${missing[*]}"
fi

installed=()
for package in "${wanted[@]}"; do
    if package_installed "$package"; then
        installed+=("$package")
    fi
done
info paquets_installes "${installed[*]}"

# ------------------------------------------------------- 4. le fichier-garde

step "le fichier-garde $GUARD_FILE"

if [ -f "$GUARD_FILE" ] && printf '%s\n' "$PACKAGES" | cmp -s - "$GUARD_FILE"; then
    step_unchanged "fichier-garde $GUARD_FILE"
else
    candidate="$GUARD_FILE.tmp"
    printf '%s\n' "$PACKAGES" > "$candidate"
    chmod "$GUARD_MODE" -- "$candidate"
    chown "$DIR_OWNER:$DIR_GROUP" -- "$candidate"
    mv -f -- "$candidate" "$GUARD_FILE"
    step_done "fichier-garde $GUARD_FILE écrit"
fi

# ------------------------------------------------------------- 5. vérifier

step "vérifier — Docker et le plugin compose par le chemin réel"

if command -v docker > /dev/null 2>&1; then
    docker_version=$(docker --version 2> /dev/null | awk '{ print $3 }' | tr -d ',' || true)
    info docker "${docker_version:-présent}"
    # Le plugin compose se demande à docker, pas à un binaire docker-compose :
    # c'est docker-compose-plugin qui est posé, et il n'a pas de commande à lui.
    compose_version=$(docker compose version --short 2> /dev/null || true)
    if [ -n "$compose_version" ]; then
        info docker_compose "$compose_version"
    else
        info docker_compose absent
        warn "le plugin compose ne répond pas : « docker compose » ne fonctionnera pas"
    fi
    # Un démon qui ne démarre pas — dans un conteneur, sans privilèges — se
    # constate, il ne fait pas échouer la pose des paquets.
    if systemctl is-active --quiet docker.service 2> /dev/null; then
        info docker_service actif
    else
        info docker_service inactif
        warn "le démon Docker n'est pas actif : aucun conteneur ne démarrera tant qu'il ne l'est pas"
    fi
else
    info docker absent
fi

# ------------------------------------------------------------------ 6. constat

if [ "$changed" -eq 1 ]; then
    done_changed
fi
done_unchanged
