#!/bin/bash
# L'en-tête commun à toute action. Go le concatène devant le corps
# (internal/scripts/scripts.go) : ce qui est déposé sur la machine est un seul
# fichier run.sh, sans rien à sourcer là-bas.
#
# SC2317 et SC2329 : c'est une bibliothèque, chaque action n'appelle qu'une
# partie de ses fonctions ; une fonction non appelée ici n'est pas une fonction
# morte. Les deux codes disent la même chose, selon la version de shellcheck.
# shellcheck disable=SC2317,SC2329
set -euo pipefail
export PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C
umask 077
# IFS par défaut, écrit noir sur blanc : un IFS hérité découperait autrement.
IFS=$' \t\n'

# Ce que le script écrit, et rien d'autre : openCloud relit ces préfixes
# (internal/catalog/types.go). Jamais une valeur dans un format printf.
step() {
    printf 'étape: %s\n' "$*"
}

info() {
    printf 'info: %s=%s\n' "$1" "$2"
}

warn() {
    printf 'avertissement: %s\n' "$*"
}

# Un refus n'est pas un échec : la cause, puis le geste qui la lève.
refuse() {
    printf 'résultat: refusé — %s\n' "$1"
    printf '→ %s\n' "$2"
    exit 2
}

fail() {
    printf 'résultat: échoué — %s\n' "$*"
    exit 1
}

done_unchanged() {
    printf 'résultat: inchangé\n'
    exit 0
}

done_changed() {
    printf 'résultat: fait\n'
    exit 0
}
