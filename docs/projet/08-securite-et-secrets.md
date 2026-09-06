# Sécurité, accès et secrets

## L'accès à l'interface

- **Jamais exposée sans authentification**, dans aucune configuration.
- **Par défaut** : identifiant et mot de passe, protection au niveau du proxy
  inverse, modifiables depuis l'interface. Un identifiant par défaut est défini
  pour tout déploiement.
- **Plus tard** : MFA et identités centralisées, par exemple Authentik. Avec
  une réserve à garder en tête : brancher openCloud sur un service d'identité le
  rend **dépendant de ce service**. Si l'annuaire tombe, l'outil qui sert à
  réparer l'infrastructure n'est plus accessible. Il faut donc toujours
  conserver un **accès local de secours**.

## Les secrets des services

**Un `.env` par service.** C'est le format que comprennent à la fois `compose`
et les cadres applicatifs PHP, il n'ajoute aucun outil, et il est lisible par
l'opérateur.

Trois conséquences qui s'enchaînent, et qu'il faut assumer ensemble :

1. le `.env` contient des secrets **en clair** → permissions strictes,
   propriétaire limité à l'utilisateur du service ;
2. il doit survivre au redéploiement → il vit sous `data/`, **pas** sous
   `workspace/` (voir `04-implantation.md`) ;
3. il fait donc partie de la sauvegarde → **la sauvegarde contient des secrets
   en clair**, ce qui rend obligatoire le chiffrement de toute copie qui sort de
   l'infrastructure (voir `07-donnees-et-sauvegardes.md`).

La boucle est cohérente, à condition de ne sauter aucun maillon.

Un quatrième geste, imposé par le terrain : **`.gitignore` forcé sur tout
`.env` généré**, quand le dossier de service est un dépôt git
(`03-modele.md`). Un `.env` poussé sur un dépôt est exploité en minutes.

## Les secrets réellement en jeu

| Secret | À quoi il sert | Qui le détient | Ce qu'une fuite permet |
|---|---|---|---|
| **Clé SSH d'openCloud** | Agir sur les machines | Machine openCloud | Tout. C'est le contrôle de l'infrastructure entière. |
| **Jetons DNS (Cloudflare)** | Poser le TXT du challenge, et sur demande un A/CNAME | Machine openCloud — **un par zone**, une seule copie chacun | Détourner **la zone de ce jeton** : rediriger le trafic, émettre des certificats à son nom. Les autres zones tiennent |
| **Clé de chiffrement des sauvegardes** | Chiffrer et restaurer | L'opérateur, dans son gestionnaire de mots de passe | Lire toutes les données de tous les services |
| **Identifiants de l'interface** | Se connecter à openCloud | Machine openCloud | Agir comme l'opérateur |
| **Accès au stockage froid** | Déposer les sauvegardes | Point de récupération | Lire ou détruire les sauvegardes |
| **`.env` d'un service** | Faire tourner l'application | La machine du service | Les données et les API de cette application seulement |

Deux enseignements :

- **Les quatre premiers ne sont pas de même nature que le dernier.** Un `.env` qui
  fuit compromet une application ; la clé SSH qui fuit compromet tout. Ils ne
  peuvent être ni stockés ni sauvegardés de la même manière.
- **La machine openCloud concentre les clés du royaume.** C'est le prix de la
  centralisation, et c'est ce qui justifie qu'elle ne serve pas de site public
  exposé si on peut l'éviter.

## Les secrets d'openCloud lui-même

openCloud tient **une petite base locale** — SQLite — sous `/var/lib/opencloud/`.
Ses propres secrets (clé SSH, jetons Cloudflare, identifiants) sont **des
fichiers à permissions strictes** au même endroit, protégés par les droits du
système. **Jamais réaffichés dans l'interface** : écrits une fois, plus jamais
relus par un humain. La copie froide étant chiffrée, ils sont illisibles hors de
la machine.

## Les comptes

**Un seul opérateur** au départ : identifiant et mot de passe par défaut,
**changés obligatoirement à la première connexion**. Le modèle de données ne
ferme pas la porte à plusieurs comptes ; on n'écrit simplement pas les rôles
maintenant. Perte du mot de passe : une procédure locale sur la machine
openCloud.

Deux précisions posées maintenant :

- **La 2FA est prévue dans le modèle de données dès aujourd'hui** — les
  colonnes existent, le code ne l'implémente pas encore. L'ajouter plus tard ne
  demandera pas de migration douloureuse.
- **Aucune politique de mot de passe n'est imposée sans porte de sortie.** Sur
  un réseau de confiance, l'opérateur peut la lever ; l'écran dit ce qu'il
  perd. Une règle sans échappatoire fait fuir plus qu'elle ne protège.
  Concrètement : **douze caractères au moins** pour un nouveau mot de passe
  (OWASP ASVS 2.1.1, compte d'administration) ; `min_password_length = 0` dans
  la configuration lève la règle, et l'écran le dit.
- **Le jeton GitHub de `self-update`** (`github_token`, tant que le dépôt est
  privé) vit dans `config.toml`, lisible par le service qui n'en a pas
  l'usage. Accepté parce que le jeton est en lecture seule, à portée minimale,
  et temporaire ; le sortir de portée du service est une réserve ouverte.

## Le durcissement des machines

| Sujet | Position |
|---|---|
| **SSH** | Clé uniquement. Modifier `sshd` sur une machine qui sert déjà peut couper l'accès de l'opérateur : ce changement se propose et se montre, jamais en silence. |
| **Pare-feu** | N'ouvrir que le nécessaire, en tenant compte d'un pare-feu déjà présent et de celui de l'hébergeur. **Vérifié depuis l'extérieur** par *Diagnostiquer*, qui **avertit sur tout port publié en `0.0.0.0`** : Docker contourne `ufw`, une règle n'est donc pas une preuve. |
| **WAF / bannissement** | **CrowdSec** : agent sur chaque machine, API centrale sur la machine openCloud, bouncer Traefik. **Une vérification périodique contrôle que le bouncer bloque réellement**, pas seulement que le service est actif. L'interface **liste les bannissements** et porte un geste **débannir** (`09-observation-et-interface.md`). |
| **Utilisateur dédié** | Un utilisateur `opencloud` par machine, jamais root direct. Son `sudo` n'autorise **qu'un lanceur root-owned**, `oc-launch <id>` (`15-catalogue-actions.md` §1). **Ce que ça protège** : l'erreur, les escapes (`systemd-run` seul en liste blanche = root, GTFOBins), les autres processus de la machine, la lisibilité de `sudo -l`. **Ce que ça ne protège pas** : une clé volée — le compte écrit lui-même les scripts qu'il fait lancer. Dit sans enjoliver (`annexes/lecture-sudoers.md`). |
| **Isolation entre environnements** | Un réseau de conteneurs et un utilisateur système par environnement. Le proxy est partagé. |

> Chaque durcissement est une **modification système** : nommée, listée,
> réversible (voir `04-implantation.md`), et jamais appliquée sans être montrée.

