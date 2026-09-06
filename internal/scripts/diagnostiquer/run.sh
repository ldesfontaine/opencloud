# shellcheck shell=bash
# Diagnostiquer : lecture seule. Rien n'est écrit, rien n'est rechargé — d'où
# « inchangé » à la fin, quel que soit le nombre de passages. L'en-tête commun
# (lib.sh) est concaténé devant celui-ci par Go.

LAUNCHER=/usr/local/sbin/oc-launch

# Un outil optionnel absent n'est pas un échec : le constat dit « absent ».
have() {
    command -v -- "$1" > /dev/null 2>&1
}

# L'espace libre d'un point de montage, en mébioctets.
free_space() {
    local path=$1 blocks
    if [ ! -d "$path" ]; then
        printf 'absent'
        return 0
    fi
    blocks=$(df -P -- "$path" 2>/dev/null | awk 'NR == 2 { print $4 }' || true)
    if [ -z "$blocks" ]; then
        printf 'inconnu'
        return 0
    fi
    printf '%s Mio' "$((blocks / 1024))"
}

step "préflight — de quoi lire la machine"
if [ ! -d /run/systemd/system ]; then
    refuse "cette machine ne tourne pas sous systemd" \
        "installer systemd, ou retirer cette machine d'openCloud"
fi
if [ ! -r /proc/uptime ]; then
    refuse "cette machine ne donne pas accès à /proc" \
        "démonter le masque sur /proc, ou lancer l'action hors d'un bac à sable"
fi

step "identité"
info hote "$(uname -n)"
info noyau "$(uname -r)"
system=$(sed -n 's/^PRETTY_NAME="\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' /etc/os-release 2>/dev/null | head -n 1 || true)
info systeme "${system:-inconnu}"
read -r uptime_seconds _ < /proc/uptime
info duree_de_fonctionnement "${uptime_seconds%%.*} s"

step "horloge"
if have timedatectl; then
    synchronized=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || true)
    info horloge_synchronisee "${synchronized:-inconnu}"
else
    info horloge_synchronisee absent
fi

step "espace et mémoire"
info espace_libre_racine "$(free_space /)"
info espace_libre_srv "$(free_space /srv)"
memory=$(awk '/^MemAvailable:/ { print int($2 / 1024) }' /proc/meminfo 2>/dev/null || true)
if [ -n "$memory" ]; then
    info memoire_libre "$memory Mio"
else
    info memoire_libre inconnu
fi

step "unités systemd"
if have systemctl; then
    failed=$(systemctl --failed --no-legend --plain 2>/dev/null | awk '{ print $1 }' || true)
    if [ -z "$failed" ]; then
        info unites_en_echec aucune
    else
        info unites_en_echec "$(printf '%s' "$failed" | tr '\n' ' ')"
    fi
else
    info unites_en_echec absent
fi

step "docker"
if have docker; then
    docker_version=$(docker --version 2>/dev/null | awk '{ print $3 }' | tr -d ',' || true)
    info docker "${docker_version:-présent}"
    if docker compose version > /dev/null 2>&1; then
        info plugin_compose présent
    else
        info plugin_compose absent
    fi
else
    info docker absent
    info plugin_compose absent
fi

step "ports"
if have ss; then
    listening=$(ss -Hlnt 2>/dev/null | awk '{ print $4 }' || true)
    for port in 80 443; do
        if printf '%s\n' "$listening" | grep -q -- ":${port}\$"; then
            info "port_${port}" tenu
        else
            info "port_${port}" libre
        fi
    done
    # Docker publie en contournant ufw : un port sur toutes les interfaces se
    # signale, il ne se corrige pas ici.
    while read -r address; do
        [ -n "$address" ] || continue
        if [ "${address#0.0.0.0:}" != "$address" ] || [ "${address#\[::\]:}" != "$address" ]; then
            warn "port publié sur toutes les interfaces : $address"
        fi
    done <<< "$listening"
else
    info port_80 absent
    info port_443 absent
fi

step "la norme d'implantation"
if [ -d /srv/workspace ]; then
    info norme_workspace présent
else
    info norme_workspace absent
fi
if [ -d /srv/data ]; then
    info norme_data présent
else
    info norme_data absent
fi

step "le lanceur"
if [ ! -e "$LAUNCHER" ]; then
    info lanceur absent
else
    info lanceur_proprietaire "$(stat -c '%U:%G' -- "$LAUNCHER")"
    info lanceur_mode "$(stat -c '%a' -- "$LAUNCHER")"
    if [ -u "$LAUNCHER" ] || [ -g "$LAUNCHER" ]; then
        warn "le lanceur porte un bit setuid ou setgid : sudo élève déjà, il n'en a jamais besoin"
    else
        info lanceur_bits_speciaux aucun
    fi
    # L'empreinte est constatée, pas comparée : openCloud ne tient pas encore
    # de référence par version.
    info lanceur_empreinte "sha256:$(sha256sum -- "$LAUNCHER" | awk '{ print $1 }')"
fi

step "vérifier — la lecture n'a rien écrit"
done_unchanged
