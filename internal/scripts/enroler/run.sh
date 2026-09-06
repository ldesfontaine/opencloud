# shellcheck shell=bash
# Enrôler : joué par l'opérateur en root sur la machine à enrôler, avec
# OC_PUBLIC_KEY dans l'environnement. Ce script n'est pas lancé par oc-launch —
# le lanceur n'est pas encore là ; il vient ensuite par SSH, une fois le compte
# en place. L'en-tête commun (lib.sh) est concaténé devant celui-ci par Go.
#
# L'ordre est une propriété de sécurité (15-catalogue-actions.md §3) : le
# compte, puis sudo, puis sshd, la clé en dernier.

ACCOUNT=opencloud
ACCOUNT_SHELL=/bin/sh
HOME_DIR=/var/lib/opencloud
ACTIONS_DIR=/var/lib/opencloud/actions
SSH_DIR=/var/lib/opencloud/.ssh
AUTHORIZED_KEYS=/var/lib/opencloud/.ssh/authorized_keys
JOURNAL_GROUP=systemd-journal
SUDOERS_FILE=/etc/sudoers.d/opencloud
# Un point dans le nom : sudo ignore ce fichier tant que visudo ne l'a pas
# validé et qu'il n'est pas renommé.
SUDOERS_CANDIDATE=/etc/sudoers.d/opencloud.tmp
SSHD_DROP_IN=/etc/ssh/sshd_config.d/opencloud.conf
SSHD_DROP_IN_PREVIOUS=/etc/ssh/sshd_config.d/opencloud.conf.previous
SSH_UNIT=ssh.service
HOST_KEY_DIR=/etc/ssh

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

# La règle sudo. Le compte opencloud n'a droit qu'au lanceur et à la commande
# exacte qui pose le lanceur : c'est elle qui le met à jour à chaque version.
# Le même texte vit dans internal/enroll/steps.go, et un test Go compare les deux.
sudoers_content() {
    cat << 'SUDOERS'
Defaults:opencloud env_reset, !setenv, !log_input, !log_stdin
opencloud ALL=(root) NOPASSWD: /usr/local/sbin/oc-launch
opencloud ALL=(root) NOPASSWD: /usr/bin/install -o root -g root -m 0755 /var/lib/opencloud/oc-launch.new /usr/local/sbin/oc-launch
SUDOERS
}

# Le drop-in sshd. « Match all » referme le bloc : sans lui, tout ce qui suit
# l'Include ne vaudrait plus que pour le compte opencloud.
sshd_drop_in_content() {
    cat << 'SSHD_DROP_IN'
# Posé par l'enrôlement d'openCloud. Le compte de service ne fait que lancer
# oc-launch : il n'a besoin de rien d'autre.
Match User opencloud
    AuthenticationMethods publickey
    PasswordAuthentication no
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    AllowTcpForwarding no
    PermitTunnel no
Match all
SSHD_DROP_IN
}

require_binary() {
    local path=$1 reason=$2
    if [ ! -x "$path" ]; then
        refuse "$path est absent : $reason" \
            "installer le paquet qui fournit $path, puis rejouer la commande"
    fi
}

# ensure_directory pose un dossier du compte de service, et ne le retouche que
# s'il n'est pas déjà tel qu'on l'attend.
ensure_directory() {
    local path=$1 mode=$2
    if [ -d "$path" ] && [ "$(stat -c '%a %U:%G' -- "$path")" = "$mode $ACCOUNT:$ACCOUNT" ]; then
        step_unchanged "$path"
        return 0
    fi
    install -d -o "$ACCOUNT" -g "$ACCOUNT" -m "$mode" -- "$path"
    step_done "$path posé en $mode, $ACCOUNT:$ACCOUNT"
}

# host_key_type : ssh_host_ed25519_key.pub donne « ed25519 ».
host_key_type() {
    local name
    name=$(basename -- "$1")
    name=${name#ssh_host_}
    printf '%s' "${name%_key.pub}"
}

# ---------------------------------------------------------------- 0. préflight

step "préflight — vérifier avant d'écrire quoi que ce soit"

if [ "$(id -u)" -ne 0 ]; then
    refuse "cette commande écrit dans /etc et crée un compte système, et elle ne tourne pas en root" \
        "la coller à nouveau : elle passe déjà par sudo"
fi
if [ ! -d /run/systemd/system ]; then
    refuse "cette machine ne tourne pas sous systemd" \
        "openCloud lance les actions par systemd-run : enrôler une machine gérée par systemd"
fi

require_binary /usr/bin/sudo "sudo élève le compte opencloud vers le lanceur"
require_binary /usr/sbin/visudo "visudo valide la règle sudo avant de la poser"
require_binary /usr/sbin/sshd "sshd sert les actions envoyées par openCloud"
require_binary /usr/sbin/useradd "useradd crée le compte de service"
require_binary /usr/sbin/usermod "usermod donne son shell et son groupe au compte de service"
require_binary /usr/bin/passwd "passwd verrouille le mot de passe du compte de service"
require_binary /usr/bin/install "install pose les dossiers du compte, puis le lanceur"
require_binary /usr/bin/ssh-keygen "ssh-keygen lit l'empreinte des clés d'hôte"

if [ ! -d /etc/ssh/sshd_config.d ]; then
    refuse "/etc/ssh/sshd_config.d est absent : cette version d'OpenSSH ne lit pas de drop-in" \
        "enrôler une Debian ou une Ubuntu dont sshd_config porte « Include /etc/ssh/sshd_config.d/*.conf »"
fi

# Un gestionnaire concurrent porte le proxy, le pare-feu et les certificats :
# openCloud ne cohabite pas avec lui (15-catalogue-actions.md §5, §6).
if [ -d /usr/local/psa ]; then
    refuse "cette machine est déjà tenue par Plesk : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "enrôler une machine qu'openCloud est seul à gérer"
fi
if [ -d /usr/local/cpanel ]; then
    refuse "cette machine est déjà tenue par cPanel : il porte le proxy, le pare-feu et les certificats — openCloud ne cohabite pas avec lui" \
        "enrôler une machine qu'openCloud est seul à gérer"
fi

public_key=${OC_PUBLIC_KEY:-}
if [ -z "$public_key" ]; then
    refuse "la commande a été jouée sans OC_PUBLIC_KEY : sans elle, openCloud ne pourrait jamais se connecter" \
        "recopier la commande entière depuis openCloud, sans la couper"
fi
# Une clé ed25519 : 68 caractères de base64, sans remplissage, et au plus un
# commentaire. Go l'a déjà validée ; c'est la seconde ligne de défense.
if [[ ! $public_key =~ ^ssh-ed25519\ [A-Za-z0-9+/]{68}(\ [^[:space:]]{1,128})?$ ]]; then
    refuse "OC_PUBLIC_KEY n'a pas la forme d'une clé publique ssh-ed25519" \
        "recopier la commande entière depuis openCloud, sans la couper"
fi

# ------------------------------------------------------------- 1. le compte

step "le compte $ACCOUNT"

if ! account_line=$(getent passwd -- "$ACCOUNT"); then
    useradd --system --home-dir "$HOME_DIR" --create-home --shell "$ACCOUNT_SHELL" -- "$ACCOUNT"
    step_done "compte $ACCOUNT créé"
elif [ "$(printf '%s' "$account_line" | cut -d: -f7)" != "$ACCOUNT_SHELL" ]; then
    usermod -s "$ACCOUNT_SHELL" -- "$ACCOUNT"
    step_done "shell du compte $ACCOUNT mis à $ACCOUNT_SHELL"
else
    step_unchanged "compte $ACCOUNT"
fi

# La deuxième colonne de « passwd -S » : L ou LK quand le mot de passe est
# verrouillé, P quand il est utilisable, NP quand il n'y en a pas.
if ! password_status=$(passwd -S -- "$ACCOUNT" 2>&1); then
    fail "lire l'état du mot de passe de $ACCOUNT : $password_status"
fi
password_state=$(printf '%s' "$password_status" | awk '{ print $2 }')
if [ "$password_state" = L ] || [ "$password_state" = LK ]; then
    step_unchanged "mot de passe du compte $ACCOUNT verrouillé"
else
    passwd -l -- "$ACCOUNT" > /dev/null
    step_done "mot de passe du compte $ACCOUNT verrouillé"
fi

# Sans systemd-journal, journalctl ne montrerait au compte que son propre
# journal : le suivi d'une action système ne verrait rien.
if ! getent group -- "$JOURNAL_GROUP" > /dev/null; then
    refuse "le groupe $JOURNAL_GROUP n'existe pas : sans lui, le compte $ACCOUNT ne peut pas lire le journal des actions" \
        "vérifier que systemd-journald est installé sur cette machine, puis rejouer la commande"
fi
if id -nG -- "$ACCOUNT" | tr ' ' '\n' | grep -qx -- "$JOURNAL_GROUP"; then
    step_unchanged "compte $ACCOUNT dans le groupe $JOURNAL_GROUP"
else
    usermod -aG "$JOURNAL_GROUP" -- "$ACCOUNT"
    step_done "compte $ACCOUNT ajouté au groupe $JOURNAL_GROUP"
fi

ensure_directory "$HOME_DIR" 700
ensure_directory "$ACTIONS_DIR" 700

# --------------------------------------------------------------- 2. sudoers

step "la règle sudo $SUDOERS_FILE"

if [ -f "$SUDOERS_FILE" ] && sudoers_content | cmp -s - "$SUDOERS_FILE"; then
    step_unchanged "règle sudo $SUDOERS_FILE"
else
    sudoers_content > "$SUDOERS_CANDIDATE"
    chmod 0440 -- "$SUDOERS_CANDIDATE"
    if ! visudo_output=$(visudo -c -f "$SUDOERS_CANDIDATE" 2>&1); then
        rm -f -- "$SUDOERS_CANDIDATE"
        refuse "visudo refuse la règle sudo d'openCloud : $visudo_output" \
            "corriger /etc/sudoers ou les autres fichiers de /etc/sudoers.d, puis rejouer la commande"
    fi
    chown root:root -- "$SUDOERS_CANDIDATE"
    mv -f -- "$SUDOERS_CANDIDATE" "$SUDOERS_FILE"
    step_done "règle sudo $SUDOERS_FILE posée"
fi

# ------------------------------------------------------------------ 3. sshd

step "le drop-in sshd $SSHD_DROP_IN"

if [ -f "$SSHD_DROP_IN" ] && sshd_drop_in_content | cmp -s - "$SSHD_DROP_IN"; then
    step_unchanged "drop-in sshd $SSHD_DROP_IN"
else
    # La configuration de sshd est remise telle qu'elle était si sshd la
    # refuse : un drop-in invalide empêcherait sshd de redémarrer plus tard,
    # sans prévenir. Un fichier qui ne finit pas par .conf n'est pas inclus.
    had_previous=0
    if [ -f "$SSHD_DROP_IN" ]; then
        cp -p -- "$SSHD_DROP_IN" "$SSHD_DROP_IN_PREVIOUS"
        had_previous=1
    fi

    sshd_drop_in_content > "$SSHD_DROP_IN"
    chmod 0644 -- "$SSHD_DROP_IN"
    # sshd -t exige ce dossier ; le service le crée en démarrant, mais sur une
    # machine où sshd n'a jamais tourné il manque.
    mkdir -p -- /run/sshd

    if ! sshd_output=$(sshd -t 2>&1); then
        if [ "$had_previous" -eq 1 ]; then
            mv -f -- "$SSHD_DROP_IN_PREVIOUS" "$SSHD_DROP_IN"
        else
            rm -f -- "$SSHD_DROP_IN"
        fi
        refuse "sshd refuse sa configuration une fois le drop-in d'openCloud posé : $sshd_output" \
            "corriger /etc/ssh/sshd_config, puis rejouer la commande — la configuration de sshd a été remise telle qu'elle était"
    fi
    rm -f -- "$SSHD_DROP_IN_PREVIOUS"
    step_done "drop-in sshd $SSHD_DROP_IN posé"

    if systemctl is-active --quiet -- "$SSH_UNIT"; then
        systemctl reload -- "$SSH_UNIT"
        step_done "$SSH_UNIT rechargé"
    else
        systemctl enable --now -- "$SSH_UNIT"
        step_done "$SSH_UNIT démarré"
    fi
fi

# ------------------------------------------------------------------- 4. la clé

step "la clé autorisée $AUTHORIZED_KEYS"

ensure_directory "$SSH_DIR" 700

if [ -f "$AUTHORIZED_KEYS" ] && [ "$(cat -- "$AUTHORIZED_KEYS")" = "$public_key" ]; then
    step_unchanged "clé autorisée $AUTHORIZED_KEYS"
else
    candidate="$AUTHORIZED_KEYS.tmp"
    printf '%s\n' "$public_key" > "$candidate"
    chmod 0600 -- "$candidate"
    chown "$ACCOUNT:$ACCOUNT" -- "$candidate"
    mv -f -- "$candidate" "$AUTHORIZED_KEYS"

    # Ce qui compte n'est pas ce qu'on a écrit, c'est ce que sshd lira.
    if [ "$(cat -- "$AUTHORIZED_KEYS")" != "$public_key" ]; then
        fail "$AUTHORIZED_KEYS ne porte pas la clé qu'openCloud vient d'y écrire"
    fi
    step_done "clé autorisée $AUTHORIZED_KEYS"
fi

# ------------------------------------------------------------ 5. les empreintes

step "les empreintes des clés d'hôte de cette machine"

operator_fingerprint=""
for host_key in "$HOST_KEY_DIR"/ssh_host_*_key.pub; do
    [ -f "$host_key" ] || continue
    # ssh-keygen ne connaît pas « -- » ; le chemin vient d'un motif fixe.
    if ! keygen_line=$(ssh-keygen -lf "$host_key" 2>&1); then
        warn "empreinte illisible pour $host_key : $keygen_line"
        continue
    fi
    fingerprint=$(printf '%s' "$keygen_line" | awk '{ print $2 }')
    key_type=$(host_key_type "$host_key")
    info "empreinte_$key_type" "$fingerprint"
    if [ "$key_type" = ed25519 ] || [ -z "$operator_fingerprint" ]; then
        operator_fingerprint=$fingerprint
    fi
done

if [ -z "$operator_fingerprint" ]; then
    fail "cette machine n'a aucune clé d'hôte dans $HOST_KEY_DIR : sshd n'en a jamais engendré"
fi

printf 'Saisir cette empreinte dans openCloud : %s\n' "$operator_fingerprint"

# ------------------------------------------------------------------ 6. constat

if [ "$changed" -eq 1 ]; then
    done_changed
fi
done_unchanged
