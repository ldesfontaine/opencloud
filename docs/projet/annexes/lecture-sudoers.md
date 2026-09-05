# Listes blanches sudoers pour un compte d'automatisation — ce que dit le terrain

Recherche du 5 septembre 2026 (sources ouvertes : man sudoers, GTFOBins, docs Ansible et EDB, blogs d'administrateurs, dépôts de wrappers).

## Exemples réels
| Qui | Règle | Piège | Source |
|---|---|---|---|
| Déploiement Capistrano | `deploy ALL=NOPASSWD: /bin/systemctl restart api.service, /usr/sbin/service nginx restart` | L'ordre des règles dans le fichier change ce qui s'applique | wannaexpresso.com/en-us/2021/06/08/capistrano-sudoers |
| EDB TPA (Postgres) | `postgres ALL=(ALL) NOPASSWD: /bin/systemctl start postgresql` (+ stop/restart/reload) | Pour le compte Ansible lui-même, la doc admet que restreindre par commande ne marche pas | enterprisedb.com/docs/tpa/latest/ansible-and-sudo |
| Le motif le plus copié | `deploy ALL=(ALL) NOPASSWD: ALL` | Annule toute idée de liste blanche | oneuptime.com 2026-02-21 ansible-become-nopasswd |
| GitLab Runner | « aussi petit que possible », scripts sudo versionnés | Rien n'empêche l'élargissement | gitlab.com/gitlab-org/gitlab-runner/-/issues/4349 |
| sudowrappers | sudoers → un wrapper Perl, jamais les binaires réels | Le wrapper devient toute la surface à auditer | github.com/ekollof/sudowrappers |
| suid_sudo | `user ALL=(root) NOPASSWD: /usr/bin/python3 -I -R /chemin/script *` | Le README admet que le filtrage par arguments « se contourne facilement » | github.com/yoiwa-personal/suid_sudo |

Aucun de ces exemples ne pose `env_reset`, `secure_path`, `NOEXEC` explicitement : le durcissement `Defaults` est un discours de guides, pas une pratique observée.

## Pièges
| Piège | Pourquoi | Source |
|---|---|---|
| `*` dans les arguments | `fnmatch` matche aussi `/` et les espaces : `python3 /opt/utils/*.py` contourné par `../../home/user/x.py` | davidhamann.de 2023/02/24 ; man7.org sudoers(5) |
| Commande sans argument listé | = tous arguments acceptés (texte officiel) | man7.org sudoers(5) |
| **`systemd-run` en liste blanche** | `sudo systemd-run -S` ou `-t /bin/sh` → shell root direct | gtfobins.org/gtfobins/systemd-run |
| `systemctl` en liste blanche | `link` + `enable --now` d'une unité fabriquée ; `SYSTEMD_EDITOR` | gtfobins.org/gtfobins/systemctl |
| `NOEXEC` | Partiel, pas infaillible | sweharris.org 2018-08-26 minimal-sudo |
| vim, tar, find, apt, docker | Shell ou lecture/écriture arbitraire dès qu'ils sont sudo-ables | gtfobins.org |
| Script cible modifiable par le compte | sudoers valide l'appel, jamais le contenu du fichier pointé | suid_sudo README |

## Le wrapper root-owned
Pas de solution officielle chez Ansible, GitLab, Capistrano : ils vivent avec des sudoers larges. Le motif existe (`sudowrappers`, `suid_sudo`) : sudoers n'autorise que le wrapper, root-owned, non modifiable, qui valide ses arguments et ce qu'il exécute. Il protège exactement ce qu'il vérifie — la vraie garantie vient du fait que ce qui est exécuté est **root-owned et hors d'atteinte du compte restreint**, pas de la règle sudoers.

## Ansible
« You cannot limit privilege escalation permissions to certain commands » — les modules tournent depuis un fichier temporaire au nom changeant (docs.ansible.com privilege_escalation). `requiretty` doit être désactivé. EDB compense par une restriction dans le temps, pas dans les commandes.

## Le débat
« La clé SSH donne déjà tout » porte sur l'authentification, pas sur ce que `sudo` autorise ensuite (vinnie.work 2023-01-28). Personne ne défend la liste blanche comme rempart contre une clé volée : elle est un **filet contre l'erreur, les autres processus de la machine, et pour l'audit** (sweharris.org).

## Verdict
1. Une liste blanche protège de quelque chose de réel et bon marché : plus de `sudo bash` par erreur, `sudo -l` et les journaux lisibles.
2. Elle ne protège pas de ce qui compte le plus : le compte `opencloud` écrit lui-même `run.sh` avant d'appeler `sudo` — sudoers ne valide jamais le contenu du fichier pointé.
3. `systemd-run` listé sans verrouiller chaque champ équivaut à root complet (GTFOBins). Regex POSIX dans sudoers : sudo ≥ 1.9.10 — Debian 12 oui (1.9.13), Ubuntu 22.04 non (1.9.9), Ubuntu 24.04 oui.
4. La forme qui tient : un **lanceur root-owned** qui ne reçoit que `<id>`, le revalide, construit l'appel `systemd-run` par `execve` sans shell ; sudoers n'autorise que lui.
5. Même ce lanceur ne ferme pas le trou n° 2 tant que `run.sh` est écrit par `opencloud` — c'est de la défense en profondeur, pas un mur.
6. À faire quand même, sans le vendre comme rempart contre une compromission du compte ou de la clé.

## Formes comparées, pour openCloud

| Forme | Ferme les escapes | Ferme « clé volée → mon script en root » | Coût | Verdict |
|---|---|---|---|---|
| `sudo` complet | non | non | zéro | Ce que font Ansible et les comptes « deploy » ; aucun filet |
| `systemd-run` en liste blanche, arguments par regex `sudoers` | oui, si chaque champ est ancré | non | zéro code, mais `sudo` ≥ 1.9.10 (pas Ubuntu 22.04) et une regex `sudoers` mal écrite = root | Possible, fragile |
| **Lanceur root-owned `oc-launch <id>`** (retenu) | oui | non | ~100 lignes de Go, testables | Le filet le plus solide à coût faible ; marche sur tout `sudo` |
| Unité `.path` root qui surveille le dossier d'actions, sans `sudo` du tout | oui | non | un déclencheur root-owned de plus, asynchrone | Même modèle de confiance que le lanceur, moins lisible (pas de code de retour immédiat) |
| Scripts root-owned sur chaque machine, mis à jour par un binaire signé | oui | **oui** (sauf le `compose` en mode expert, root par nature) | un binaire à mettre à jour sur chaque machine à chaque version | Le seul mur ; écarté pour l'instant, porte ouverte |

Un lanceur derrière `sudo` **n'est pas setuid** : aucun bit spécial, il n'est
root que quand `sudo` le lance. La surface d'attaque d'un setuid (exécutable
par tous, sensible à l'environnement) ne s'applique pas.
