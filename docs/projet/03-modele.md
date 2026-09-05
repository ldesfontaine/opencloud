# Le modèle — objets et règles

## Les objets

| Terme | Définition retenue |
|---|---|
| **Infrastructure** | L'ensemble des machines pilotées par une installation d'openCloud. Il n'y en a qu'une. |
| **Environnement** | Rôle d'exécution : `prod`, `preprod`, `test`… Plusieurs environnements du même rôle sont permis. |
| **Machine** | Un hôte Linux enrôlé. |
| **Machine openCloud** | La machine de l'infrastructure qui héberge openCloud lui-même. C'est un rôle, pas un type de machine (voir `02-roles.md`). |
| **Service** | Une unité déployée : conteneur, groupe de conteneurs, ou application servie par une pile classique. |
| **Domaine** | Un nom exposé par un service et servi par une machine. C'est l'objet que portent l'hôte virtuel, l'enregistrement DNS et le certificat (voir `06-reseau-et-certificats.md`). |

## Les règles qui les lient

- Une infrastructure contient **1 à N machines**, sous **Debian ou Ubuntu**.
- Une machine porte **1 à N environnements**.
- **Un service appartient à un environnement** — par défaut. Le même site en
  `prod` et en `preprod`, ce sont deux services. Ce n'est pas une interdiction :
  qui veut partager un service entre environnements peut le faire, openCloud
  ne met pas de bâton dans les roues.
- **L'environnement est dans le chemin** : `/srv/workspace/<env>/<service>/`.
  Lisible à la main, sans openCloud.
- Deux environnements sur une même machine **partagent le proxy Traefik** —
  un seul par machine. Ils ont **chacun leur réseau de conteneurs et leur
  utilisateur système** ; c'est ce qui rend la séparation réelle. La politique
  de sauvegarde se règle par environnement.
- Un service expose **0 à N domaines**. **Uniquement du HTTP** : un port brut
  (courrier, base, jeu) est hors périmètre.

## Le dossier d'un service — la convention

Chaque service est un dossier sous la norme, qui contient **ce qu'il faut pour
le faire vivre sans openCloud** :

```
/srv/workspace/<env>/<service>/
├── compose.yaml        # ou la configuration du site classique
├── Makefile            # les cibles standard, ci-dessous
└── .env                # lien vers /srv/data/<env>/<service>/.env
```

**openCloud appelle les cibles du Makefile. À la main, on appelle les mêmes.**
C'est ce qui garantit que l'outil et l'opérateur font exactement la même chose.

**Si le dossier de service est un dépôt git, openCloud force `.gitignore` sur
tout `.env` qu'il génère.** Un `.env` poussé sur un dépôt est exploité en
minutes ; le geste coûte une ligne (`08-securite-et-secrets.md`).

### Les cibles standard

Le même contrat pour les deux formes de service ; seule la mise en œuvre
change.

| Cible | Conteneur | Site classique | Règle |
|---|---|---|---|
| `up` | `compose up -d --remove-orphans` | recharge `php-fpm` et le vhost | Idempotent |
| `down` | `compose down` | désactive le vhost | **Ne touche jamais aux volumes ni aux données** |
| `restart` | `compose restart` | recharge `php-fpm` | |
| `status` | `compose ps` | état du pool et du vhost | Lecture seule |
| `logs` | `compose logs --tail=200 -f` | journald du pool | Lecture seule |
| `config` | `compose config` — valide la configuration rendue | vérifie le vhost (`nginx -t` / `apachectl -t`) | Zéro écriture. openCloud l'appelle **avant** `up` et `update` |
| `pull` | `compose pull` | `git fetch` | Télécharge, n'applique pas |
| `update` | `pull` + `up -d` + `image prune` des images remplacées | `git pull` + `composer install --no-dev` + migrations + recharge | **La mise à jour du service.** Précédée d'un `backup` si openCloud le demande |
| `build` | `compose build --pull` | — | Pour les images construites sur place |
| `shell` | `compose exec <svc> sh` | shell sous l'utilisateur du site | Pour l'opérateur, pas pour openCloud |
| `backup` | arrêt si nécessaire, archive de `/srv/data/<env>/<service>/`, extraction cohérente des bases | idem | Écrit dans `/srv/data/backups/` |
| `restore` | l'inverse, depuis un slot nommé | idem | Irréversible, confirmé |
| `clean` | `down` + suppression des images orphelines | — | **Jamais appelé par openCloud.** Ne supprime pas les volumes |

Deux règles qui ne se discutent pas : **`down` et `clean` ne détruisent jamais
de données** — effacer est un geste séparé ; et **`update` ne s'exécute jamais
seul** — c'est une action, approuvée.

Un `Makefile.common` fourni par openCloud porte les cibles génériques ; le
`Makefile` du service l'inclut et ne redéfinit que ce qui lui est propre.

## Les deux formes de service

Décision prise : **les deux formes sont servies**. C'est plus de travail, mais
la seconde est ce qui tourne réellement chez beaucoup d'hébergements, et
l'exclure reviendrait à écrire un outil qui ne sert que les cas neufs.

| Forme | Ce que c'est | Ce qu'openCloud manipule |
|---|---|---|
| **Conteneurisée** | Une ou plusieurs images, des volumes, des variables | Une définition `compose`, des volumes sous la racine de données |
| **Classique** | Une application servie depuis un chemin par PHP/Apache/nginx | Une racine de site par environnement, un hôte virtuel, une base |

### La question des versions PHP

Le vrai sujet de la forme classique : **deux sites sur la même machine n'ont
pas la même version de PHP.** La réponse est connue, et elle se joue à deux
endroits distincts.

- **Côté serveur web** — plusieurs versions de PHP-FPM installées côte à côte
  (`php8.1-fpm`, `php8.3-fpm`, …), **un pool par site**, et l'hôte virtuel qui
  pointe vers le bon socket. C'est la solution standard ; elle isole au passage
  les sites entre eux par l'utilisateur du pool.
- **Côté ligne de commande** (`composer`, une commande d'administration de
  l'application, une tâche planifiée applicative) — c'est un problème
  *différent*, et le `.bashrc` n'est pas le bon
  outil : il ne vaut que pour un interpréteur interactif, donc ni pour les
  tâches planifiées ni pour les scripts. La voie propre est un chemin absolu
  vers le bon binaire (`/usr/bin/php8.1`) dans chaque commande, ou un petit
  script par site qui fixe la version.

Trois pièges connus, que le script traite nommément :

| Piège | Ce que le script fait |
|---|---|
| Socket du pool en `0660` mal aligné | Aligne propriétaire et groupe du socket sur l'utilisateur du serveur web, et le vérifie après coup |
| `php -v` ne dit pas la version de FPM | Lit la version du pool, jamais celle de la ligne de commande |
| OPcache partagé entre versions | Un réglage OPcache **par version** de FPM, jamais un réglage global |

## Ce que contient la déclaration d'un service

**Deux façons de déclarer** :

- **Le formulaire** — par défaut. Nom, environnement, machine, image ou chemin,
  domaines, volumes, variables. openCloud écrit le `compose` ou l'hôte virtuel.
  Il comprend ce qu'il déploie : il sauvegarde et affiche correctement.
- **Le mode expert** — un `compose` fourni tel quel. openCloud le pose et le
  lance. Ce qu'il perd est dit à l'écran : pas de sauvegarde fine, un état
  brut.

Le contrat minimal, à figer avant toute écriture :

- **nom**, **environnement**, **machine** ;
- **forme** — conteneur ou chemin ;
- **source** — image et version, ou dépôt et référence ;
- **domaines exposés** (0 à N) ;
- **chemins persistants** — ce qui survit au redéploiement ;
- **variables** et **secrets** ;
- **dépendances** — base de données, cache ;
- **politique de sauvegarde** ;
- éventuellement des **limites de ressources**.

**Une base de données est un service comme un autre**, déployé par openCloud :
il sait la poser, la sauvegarder par extraction cohérente, la restaurer.

## Ce qu'on fait d'un service

Déclarer · déployer · redéployer dans une autre version · arrêter et démarrer ·
supprimer.

**Tout conteneur présent sur la machine reçoit les actions génériques** —
démarrer, arrêter, redémarrer, journaux, hôte virtuel — qu'openCloud l'ait posé
ou non. Il faut seulement retrouver l'identifiant du conteneur, ce qui est peu.

**La suppression est le point sensible**, et la règle est nette : **les données
ne partent jamais avec le service.** Effacer les données reste un geste séparé
et explicite.

Les attributs de chacune de ces actions — portée, lieu, réversibilité,
interruption — sont dans `05-execution.md`.

