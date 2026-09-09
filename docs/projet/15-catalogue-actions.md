# Le catalogue d'actions, et comment elles s'exécutent sans injection

Matériel : le code de `your-cloud` (séquences éprouvées, refus, regex), le code
de SysWarden (installation par étapes, marqueur de suppression, règles
d'exécution). Références : `annexes/lecture-syswarden.md`.

## 1. Le modèle d'exécution

### Une action, c'est trois fichiers

| Fichier | Qui l'écrit | Contenu |
|---|---|---|
| `run.sh` | Le dépôt openCloud, versionné | Le script. Jamais modifié à la volée. Empreinte connue. |
| `params.env` | openCloud (Go), après validation | `OC_DOMAINE=exemple.com` — une ligne par paramètre, valeurs validées, quoting fait par Go. Mode `0600`. |
| `files/` | openCloud (Go), rendu par gabarit | Les fichiers de configuration déjà rendus (fragment Traefik, `compose.yaml`, unité). Le script les **pose**, il ne les **compose** pas. |

Déposés sous `/var/lib/opencloud/actions/<id>/`, puis lancés par un vecteur
**fixe** dont le seul élément variable est l'identifiant `[0-9a-z-]` :

```
sudo -n /usr/local/sbin/oc-launch <id>
```

**`oc-launch` est un petit binaire root-owned, non modifiable**, posé à
l'enrôlement. Il revalide `<id>` (`^[0-9a-z-]{1,40}$`), lit le délai maximum
dans le dossier de l'action, puis exécute lui-même, par `execve` et sans
shell :

```
/usr/bin/systemd-run --unit=oc-action-<id> \
  --property=EnvironmentFile=/var/lib/opencloud/actions/<id>/params.env \
  --property=RuntimeMaxSec=<n> --collect \
  /var/lib/opencloud/actions/<id>/run.sh
```

Pourquoi un lanceur et pas `systemd-run` en liste blanche : `sudo systemd-run
-t /bin/sh` donne un shell root (GTFOBins). Le lanceur ne reçoit qu'un
identifiant, rien d'autre.

**Le lanceur, durci** — ce n'est pas un binaire setuid : c'est `sudo` qui
élève, le fichier n'a aucun bit spécial.

| Règle | Pourquoi |
|---|---|
| `root:root`, `0755`, sans setuid ni setgid, dans `/usr/local/sbin` | Un setuid est exécutable par tout le monde et fragile aux variables d'environnement ; un binaire ordinaire derrière `sudo` ne l'est pas |
| Statique, sans dépendance, ~100 lignes | Moins de code, moins de surface |
| **Un seul argument**, `^[0-9a-z-]{1,40}$`, tout le reste refusé | Pas d'option, pas de chemin, pas de valeur libre |
| Ignore l'environnement ; `sudoers` pose `env_reset` et `secure_path` pour l'utilisateur | Rien d'hérité ne change son comportement |
| Chemins absolus construits en dur ; `execve` de `/usr/bin/systemd-run` seul, jamais de shell | Pas de résolution `PATH`, pas d'interpolation |
| Ouvre le dossier de l'action avec `O_NOFOLLOW`, refuse les liens symboliques, exige un fichier régulier appartenant à `opencloud` et non modifiable par les autres | Ferme les tours de passe-passe par lien vers un fichier root |
| Posé à l'enrôlement ; *Diagnostiquer* vérifie propriétaire, mode et empreinte | Un lanceur modifié se voit |
| Ligne `sudoers` : `opencloud ALL=(root) NOPASSWD: /usr/local/sbin/oc-launch` ; sur `sudo` ≥ 1.9.10, la forme regex `^/usr/local/sbin/oc-launch [0-9a-z-]{1,40}$` | Debian 12 et Ubuntu 24.04 ont la regex ; Ubuntu 22.04 non — le lanceur valide de toute façon |
| **Deux lignes `sudoers`, pas une** : la seconde est `install -o root -g root -m 0755 /var/lib/opencloud/oc-launch.new /usr/local/sbin/oc-launch`, à arguments fixes | C'est elle qui pose le lanceur sur une machine distante, et qui le met à jour ensuite. Aucun joker : `install` n'y écrit qu'à ce chemin-là, depuis ce fichier-là |

Ce qu'il ne ferme pas, une fois pour toutes : le compte `opencloud` écrit
`run.sh`. Qui détient sa clé peut faire exécuter son script en root. Ce trou ne
se ferme qu'avec des scripts root-owned sur chaque machine, écarté pour
l'instant (`annexes/lecture-sudoers.md`, formes comparées).

> **Aucune valeur saisie par l'opérateur ne passe par une ligne de commande.**
> Elles vont dans `params.env` (lu par systemd, pas par un shell) et dans
> `files/` (rendus par Go). C'est ce qui rend l'injection impossible par
> construction, pas par vigilance.

**Ce que le lanceur protège, et ce qu'il ne protège pas** : il ferme les
escapes et l'erreur humaine, il rend `sudo -l` lisible. Il **ne protège pas**
d'une clé volée : c'est `opencloud` qui écrit `run.sh` avant d'appeler `sudo`,
et `sudoers` ne valide jamais le contenu d'un fichier. Le fermer exigerait des
scripts root-owned sur chaque machine, donc un binaire à mettre à jour partout
à chaque version — écarté pour l'instant (`annexes/lecture-sudoers.md`).

La machine openCloud sur elle-même : même vecteur, par `ssh` vers `localhost`
(`05-execution.md`).

### Les règles, toutes reprises de your-cloud et de SysWarden

| Règle | Origine |
|---|---|
| **Jamais de shell interpolé** côté openCloud : `exec.Command(chemin, args...)`, environnement remplacé (`PATH` fixe, `LC_ALL=C`), `Dir=/` | your-cloud `system.go`, SysWarden `upgrade.go` |
| **Binaires par chemin absolu**, jamais de résolution `PATH` | SysWarden |
| **Chaque paramètre a un type et une borne** (§4), validé en Go avant tout. Ce qui ne correspond pas est un refus nommé | your-cloud `schema2.go` |
| **Les chemins sont dérivés des noms, jamais saisis** | your-cloud `route.go` |
| **Avant lancement** : l'empreinte de `run.sh` est vérifiée ; le script et son dossier ne sont pas modifiables par un tiers | SysWarden `firewall_linux.go` |
| **Client SSH borné** : `-F /dev/null`, `IdentitiesOnly`, `BatchMode`, `StrictHostKeyChecking=yes`, `known_hosts` dédié, `ClearAllForwardings`, `RequestTTY=no`, `ConnectTimeout`. Environnement vide | your-cloud `command_launch.go` |
| **Délai maximum par type d'action**, appliqué par la machine (`RuntimeMaxSec`) ; sortie bornée en taille côté openCloud | SysWarden |
| **Dans le script** : `set -euo pipefail`, `PATH` et `LC_ALL` fixés en tête, toutes les variables entre guillemets, `--` avant les positionnels, jamais `eval`, jamais `sh -c`, jamais de valeur dans un `printf` de format | your-cloud (piège `stat -c`) |
| **Fichiers posés atomiquement** : temporaire, `fsync`, `rename` | SysWarden, your-cloud |

### Ce que tout script fait, dans cet ordre

1. **Préflight** — vérifier avant la première écriture. Tout manque est un
   **refus nommé** : cause + geste qui la lève. Code de retour `2`.
2. **Lever le drapeau « machine touchée »** avant le premier effet — un
   `useradd` interrompu laisse autant qu'un `useradd` réussi.
3. **Comparer** — un état identique octet pour octet n'est pas une action : ni
   réécriture, ni redémarrage. Sortie `inchangé`, code `0`. **Propriété
   testée** : rejoué deux fois en CI, le script sort `inchangé` la seconde fois
   (`17-conventions-code.md`).
4. **Écrire, recharger** — une ligne par étape sur la sortie standard, préfixée
   `étape:`.
5. **Vérifier par le chemin réel** — une requête HTTPS, un `systemctl
   is-active`, un fichier lu, jamais la sortie d'un outil comme preuve.
6. **Constat final** — une ligne `résultat:` puis le code : `0` fait, `1`
   échoué, `2` refusé.

Le journal de transaction d'openCloud (`05-execution.md`) enregistre `préparée`
avant l'étape 1 et conclut après l'étape 6.

### Ce que l'écran montre avant de lancer

Trois choses, pas une :

| Ce qui est montré | D'où ça vient |
|---|---|
| Les **quatre attributs** — portée, lieu, réversibilité, interruption | `05-execution.md` |
| Le **diff des fichiers de `files/`** contre ce qui est sur la machine | Rendu par Go, comparé avant la pose |
| Le **résultat de la validation à blanc** | `make config`, `sshd -t`, `visudo -c` selon l'action |

Quatre étiquettes ne disent pas ce qui va être écrit. Le diff, si.

## 2. Le catalogue

Colonnes : **portée** · **paramètres** · **irréversible** · **coupe le service**.
Toutes s'exécutent sur la machine cible sauf mention.

### Machine

| Action | Portée | Paramètres | Irrév. | Coupe | Notes |
|---|---|---|---|---|---|
| **Enrôler** | machine | adresse, port, compte initial, empreinte d'hôte, `derrière_nat` | non | non | Commande générée, jouée par l'opérateur. **Ne touche ni au proxy ni aux certificats des autres machines**, et **vérifie après coup** que leurs hôtes virtuels répondent encore. Séquence §3. |
| **Tester l'accès** | machine | — | non | non | `ssh true`, `sudo -n true`, `systemd-run --version`. Le bouton de la fiche machine — et **une exécution périodique, en silence**, qui alimente le statut à quatre états (`02-roles.md`). |
| **Diagnostiquer** | machine | — | non | non | Lecture seule : services, disque, horloge, ports 80/443, versions. **Pare-feu vérifié depuis l'extérieur**, et **avertissement sur tout port publié en `0.0.0.0`** — Docker contourne `ufw`. **Tourne périodiquement**, compare au **dernier état connu** et **signale l'écart sans le corriger**. Sortie structurée. *(SysWarden `audit`)* |
| **Poser le socle** | machine | — | non | non | Répertoires de la norme, puis les paquets d'une **liste versionnée dans le dépôt** (un fichier embarqué dans le binaire ; ajouter une ligne suffit pour que la prochaine pose l'installe partout) : `docker.io`, `docker-compose-plugin`, `screen`, `ncdu`, `at`, `curl`, `htop`, `rsync`, `jq`. Paquets apt seulement : un outil hors apt se pose à la main. Fichier-garde d'idempotence. |
| **Installer le proxy** | machine | — | non | non | Traefik, config statique, résolveur DNS-01. Séquence §3. |
| **Installer CrowdSec** | machine | — | non | non | Agent + bouncer Traefik, enregistré auprès de l'API sur la machine openCloud. Pose aussi la **vérification périodique que le bouncer bloque réellement** — une requête de test, pas un `systemctl is-active`. |
| **Installer le collecteur** | machine | — | non | non | Métriques + Alloy vers Loki. Compte système distinct. **Refus si la machine n'atteint pas le plancher de ressources** (ordre de grandeur, 2 Go libres) : une autre machine est proposée (§6, `02-roles.md`). |
| **Débannir** | machine | adresse | non | non | Retire une décision CrowdSec, nommément. La liste des bannissements vit dans l'interface (`09-observation-et-interface.md`). |
| **Monter WireGuard** | machine | rôle, clé publique du pair, sous-réseau | non | non | Par l'enrôlement en NAT, ou seul. Séquence §3. |
| **Mettre à jour le système** | machine | — | non | possible | `apt-get` par chemin absolu, `DEBIAN_FRONTEND=noninteractive`. Signale si redémarrage requis, **et tout redémarrage non demandé**. **Liste les fichiers de configuration en conflit** (`.dpkg-dist`, `.ucf-dist`) sans les fusionner (`10-cycle-de-vie.md`). |
| **Redémarrer** | machine | — | non | **oui** | Confirmé. **Après le retour, vérifie que le proxy est reparti** et que les hôtes virtuels répondent. |
| **Retirer une machine** | machine | — | **oui** | **oui** | Marqueur « retrait en cours » écrit d'abord ; recensement avant/après ; ordre inverse de la pose. *(SysWarden tombstone, your-cloud census)* |

### Domaine

| Action | Portée | Paramètres | Irrév. | Coupe | Notes |
|---|---|---|---|---|---|
| **Créer un hôte virtuel** | domaine | nom, service cible, port lu sur la machine | non | non | Fragment rendu par Go, posé dans `data/traefik/`, rien à recharger (`providers.file.watch`). Vérifie en HTTPS local, SNI + `Host`. **Constate la résolution du nom** ; si elle ne pointe pas vers cette machine, **propose** *Créer l'enregistrement DNS*. |
| **Supprimer un hôte virtuel** | domaine | nom | non | **oui** | Retire fragment et certificat. |
| **Créer l'enregistrement DNS** | domaine + DNS | nom, type (`A` ou `CNAME`), valeur, zone | non | non | Sur la machine openCloud, par l'API Cloudflare. **Sur demande, jamais automatique, toujours montrée** avant écriture. La deuxième et dernière écriture DNS d'openCloud (`06-reseau-et-certificats.md`). |
| **Demander un certificat** | domaine + DNS | nom | non | non | TXT `_acme-challenge` chez Cloudflare. **Préflight DNS + CAA + budget Let's Encrypt** (§6). Mécanisme de dépôt : ouvert (`11`). |
| **Renouveler** | domaine + DNS | nom | non | non | Le bouton de la page certificats. Même préflight. **Refuse pendant la fenêtre de blocage Let's Encrypt** et dit l'heure de déblocage. |
| **Faire tourner un jeton DNS** | domaine + DNS | zone, nouveau jeton | non | non | Un jeton par zone (`06-reseau-et-certificats.md`). Sur la machine openCloud : pose le nouveau, **le vérifie sur la zone**, puis retire l'ancien. Un jeton n'est jamais réaffiché, donc il ne se remplace pas à la main. |

### Service

| Action | Portée | Paramètres | Irrév. | Coupe | Notes |
|---|---|---|---|---|---|
| **Déclarer** | service | le contrat (`03-modele.md`) | non | non | Aucun effet sur la machine. Enregistré en base. |
| **Générer les secrets** | service | clés manquantes | non | non | 32 octets aléatoires → hex, `O_EXCL`, jamais remplacés. `.env` sous `data/`. *(your-cloud)* |
| **Déployer** | service | version, digest | non | brièvement | Dépose `compose.yaml` + `Makefile`, `make config` puis `make up`. Digest résolu et rapporté ; `latest` refusé. |
| **Mettre à jour** | service | sauvegarde d'abord (oui/non) | non | brièvement | `make backup` si demandé, `make config`, `make update`. **Pas d'action de retour arrière** : redéployer une version antérieure suffit. L'écran le dit, dit que **les données ne reviennent pas**, et propose `backup` avant. |
| **Démarrer / arrêter / redémarrer** | service | — | non | **oui** (arrêt) | `make up` / `make down`. Vaut pour tout conteneur de la machine, posé par openCloud ou non. |
| **Journaux** | service | lignes | non | non | `make logs` → journald. Lecture seule. |
| **Supprimer** | service | — | **oui** | **oui** | Exige que ses domaines soient retirés d'abord (refus sinon). Les données restent. |

### Sauvegarde

| Action | Portée | Paramètres | Irrév. | Coupe | Notes |
|---|---|---|---|---|---|
| **Configurer la sauvegarde** | service ou machine | fréquence, plancher | non | non | Pose la minuterie systemd et la règle des 10 %. |
| **Sauvegarder maintenant** | service | — | non | selon | Arrêt si nécessaire, archive dans un fichier temporaire, digest, `rename`, remise dans l'état trouvé. Slot immuable. **Le slot d'une base porte le nom réel de la base**, pas celui du service. Enchaîne *Tester la restauration*. *(your-cloud)* |
| **Tester la restauration** | service | slot | non | non | **Jouée automatiquement après chaque sauvegarde** : restauration dans un dossier temporaire, vérification, suppression. Ne touche jamais aux données en place. Un échec est une alerte (`07-donnees-et-sauvegardes.md`). |
| **Restaurer** | service | slot | **oui** | **oui** | Déballer dans un dossier frais, archiver l'état remplacé dans `previous`, deux `rename`. Confirmé, point de non-retour nommé. |
| **Vérifier la clé de chiffrement** | infrastructure | clé fournie par l'opérateur | non | non | Périodique : déchiffre une archive de test et **ne conserve pas la clé**. Sur la machine openCloud (`07-donnees-et-sauvegardes.md`). |
| **Récupérer les sauvegardes froides** | infrastructure | — | non | non | Sur la machine openCloud, tire par `rsync`. Affiche la date. |

### Ce que le Makefile d'un service expose

`up` · `down` · `restart` · `status` · `logs` · `config` · `pull` · `update` ·
`build` · `shell` · `backup` · `restore` · `clean` — contrat et règles dans
`03-modele.md`. openCloud n'appelle que ces cibles, jamais `shell` ni `clean`.

## 3. Séquences éprouvées, reprises de your-cloud

**Enrôler** — l'ordre est une propriété de sécurité : créer le compte
(`useradd --system`, `passwd --lock`) → règle `sudo` en drop-in puis
**`visudo -c -f`** → drop-in `sshd_config.d` (`PermitTTY no`,
`X11Forwarding no`, `AllowAgentForwarding no`, `AllowTcpForwarding no`,
`PermitTunnel no`) puis **`sshd -t`** avant `reload` → clé **en dernier** →
relire après coup, trois essais, le rechargement ferme le port un instant. La
séquence est écrite une seule fois, dans le script : la machine openCloud le
joue sur elle-même (`enroll-local`), une machine distante le reçoit collé.

**Le lanceur ne voyage pas dans la commande** — il fait plusieurs mébioctets.
Sur la machine openCloud, `enroll-local` le prend dans le paquet. Sur une
machine distante, il vient **ensuite, par SSH**, une fois le compte en place et
l'empreinte confirmée : déposé sous `/var/lib/opencloud/oc-launch.new`, puis
posé par `sudo` avec une règle à arguments fixes, sans joker. La même règle le
met à jour à chaque version.

**Enrôler ne touche ni au proxy ni aux certificats des autres machines.** Rien
dans la séquence ne sort de la machine enrôlée.

En fin d'action, openCloud **vérifie que les hôtes virtuels des autres machines
répondent encore** : ajouter une machine ne casse pas celles qui tournent
(`10-cycle-de-vie.md`).

**Installer le proxy** — tirer l'image par digest **avant** qu'un fichier la
nomme → créer les répertoires **avant** qu'un montage les nomme → écrire la
config statique **avant** que le service qui la lit démarre → démarrer →
vérifier : 443 répond **404** à un nom inconnu, 80 répond **301** vers HTTPS,
lu et non suivi.

**Créer un hôte virtuel** — refus si pas de proxy ; refus si le port n'est pas
celui d'un service présent (lu dans sa définition, jamais dans une socket qui
écoute) ; comparer le fragment octet pour octet ; poser ; vérifier en HTTPS sur
`127.0.0.1:443` avec le nom en SNI **et** en `Host`, quinze essais à une
seconde.

**WireGuard** — relire le rôle déjà tenu ; générer la clé si absente, jamais la
remplacer (`O_EXCL`, `0640`, dossier `0750`) ; écrire la config ; activer le
service **avant** de recharger ; vérifier l'interface en lisant
`/sys/class/net/<if>/flags`, pas la sortie d'un outil.

**Sauvegarder** — refus si rien à archiver ou slot déjà pris ; arrêter si
nécessaire ; `tar` vers un temporaire, hacher, `rename` ; remettre **dans
l'état trouvé** ; prouver que ça répond. **Restaurer** — déballer d'abord dans
un dossier frais, archiver l'état remplacé, puis les deux `rename`. Dans l'autre
sens, restaurer `previous` détruirait ce qu'il vient de remplacer.

## 4. Les paramètres — types et bornes

| Paramètre | Forme |
|---|---|
| nom de domaine | `^[a-z0-9][a-z0-9.-]{1,251}[a-z0-9]$` — ni guillemet, ni antislash, ni espace |
| port | entier `1024`–`65535` |
| nom de service, d'environnement | `^[a-z][a-z0-9-]{0,31}$` |
| compte système | `^[a-z_][a-z0-9_-]{0,31}$` |
| slot de sauvegarde | `^[a-z0-9-]{1,32}$` — jamais `.` ni `..` |
| adresse, sous-réseau | `netip.ParseAddr` / `ParsePrefix`, jamais une regex |
| digest d'image | `^sha256:[0-9a-f]{64}$` ; un tag est refusé |
| identifiant d'action | `^[0-9a-z-]{1,40}$` |
| chemin | **jamais un paramètre** — toujours dérivé du nom |

## 5. Les refus nommés, réutilisables tels quels

- « aucun service de cette machine n'écoute sur `127.0.0.1:<port>` : une route
  vers un port que rien ne gère est refusée avant tout effet » ;
- « cette machine ne tient aucun proxy : une route est servie par l'entrée et
  ne peut être publiée avant elle » ;
- « cette machine publie encore N route(s) : retirer le proxy cesserait de les
  servir — les routes se retirent d'abord, chacune par son action » ;
- « le slot `x` tient déjà une archive : les sauvegardes sont immuables » ;
- « le slot `x` ne tient aucune archive : il n'y a rien vers quoi revenir » ;
- « cette machine ne tient aucune donnée à `<chemin>` : rien à archiver » ;
- « cette machine tient une valeur qu'elle n'a pas générée pour cette clé » —
  la clé est nommée, jamais le contenu ;
- « `image` nomme un tag, jamais un digest » ;
- « le nom fait N octets et son fragment en demanderait N+5, plus que les 255
  d'un nom de fichier » ;
- `sudo` : `a password is required` et `a terminal is required` — séparés,
  aucun secret ne fabrique un terminal ;
- « le port 80 ou 443 est déjà tenu par `<processus>` » — pas d'adaptation ;
- « cette machine est déjà tenue par `<Plesk|cPanel>` : il porte le proxy, le
  pare-feu et les certificats — openCloud ne cohabite pas avec lui » ;
- « un `certbot` est actif sur cette machine : deux clients ACME se
  disputeraient le renouvellement du même domaine » ;
- « `<domaine>` ne pointe pas vers cette machine : créer l'enregistrement
  A/CNAME, ou corriger la zone » ;
- « l'enregistrement CAA de `<domaine>` n'autorise pas Let's Encrypt » ;
- « Let's Encrypt bloque `<domaine>` jusqu'à `<heure>` : N tentatives sur la
  fenêtre, il n'en reste aucune » ;
- « cette machine n'a que `<n>` Mo libres, le composant central en demande
  2 Go : `<autre machine>` a la place ».

## 6. Préflight commun, avant toute écriture

systemd présent (`/run/systemd/system`) · cgroup v2 lu du noyau · Docker et le
plugin compose présents · binaires nommés par chemin absolu présents · horloge
synchronisée (`timedatectl show -p NTPSynchronized`) · espace disque au-dessus
du plancher · ports 80/443 libres pour le proxy.

Trois préflights particuliers s'y ajoutent :

| Avant quoi | Ce qui est vérifié |
|---|---|
| **Toute installation sur une machine** | Aucun gestionnaire présent (Plesk, cPanel), aucun `certbot` actif, ports 80/443 libres. **Le refus nomme lequel des trois** l'a arrêté (`04-implantation.md`) |
| **Installer un composant central** (Loki, collecteur central) | **Plancher de ressources** — ordre de grandeur, 2 Go libres. En dessous : refus, et **proposition d'une autre machine** (`02-roles.md`) |
| **Demander ou renouveler un certificat** | **DNS** — le nom pointe ici · **CAA** — Let's Encrypt autorisé · **budget Let's Encrypt** — tentatives récentes, ce qui reste, heure de déblocage (`06-reseau-et-certificats.md`) |
