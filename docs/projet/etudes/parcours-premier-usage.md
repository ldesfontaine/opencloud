# Parcours du premier usage : un admin sys qui découvre openCloud

> Ticket #438. Audit d'usage, documentation seule : aucun code du produit
> n'a changé. Banc et mesures du 1er novembre 2026, sur `dev` à `91c7d0d`
> (binaire `v0.0.1`). Captures, script et mesures :
> [`parcours/`](parcours/).

## En deux mots

| | |
| --- | --- |
| **Verdict** | Une fois dans l'interface, les gestes sont courts (1 à 8 clics par tâche) et les assistants tiennent à l'écran. **Les blocages sont avant et autour** : installer l'agent, lui donner un droit, retrouver un menu cité par le texte. |
| **Menus déroulants derrière les fenêtres** | **Corrigé partout où on a regardé** (F1) : 39 listes ouvertes dans 21 écrans ou fenêtres, aucune n'est masquée. |
| **Ce qui revient sur beaucoup d'écrans** | Un droit manquant se dit en phrase, jamais en geste ; des renvois de menu qui ne sont pas des liens (et deux sont faux) ; des tuiles muettes ; des pavés de 30 à 60 mots. |
| **Hors de cette étude** | Services (#437), Sauvegardes (#366), F1 à F3 : une ligne chacun, [plus bas](#déjà-couvert). |

## Méthode, et ce qu'elle ne prouve pas

Source unique : `README.md` et `docs/installation.md`, branche à jour de `dev`.
Rôle joué : un admin sys qui n'a jamais vu l'outil.

| Sujet | Ce qui a été fait | Limite |
| --- | --- | --- |
| Serveur | `make build`, puis `opencloud serve` avec la configuration du README (dossier d'état temporaire, port `18480`, mode d'accès local, pas de SSO) | La VM n'a ni `root`, ni `sudo` : pas de `useradd`, pas d'unité système. Le serveur et les agents tournent en `systemd-run --user` |
| Machine 1 (`orca`) | Agent enrôlé avec la ligne que l'écran affiche, sous mon compte | Pas de compte `opencloud-agent`, pas de lanceur `opencloud-guard` |
| Machine 2 (`vps-lyon`) | Seconde machine sur la même VM : `machine-id`, `boot_id` et nom d'hôte propres, dans un espace de noms (`unshare`) | L'agent y tourne en « root » d'un espace de noms : **ses droits d'écriture sont coupés** (`state dir is not safe`). Un artefact du banc, pas un défaut |
| Docker | **Absent de la VM.** Un faux démon ([`fake-docker.mjs`](parcours/fake-docker.mjs)) répond aux seules requêtes que l'agent lit : le projet Compose `demo-shop` (`web`, `db`) et un conteneur seul (`grafana-solo`) | **Aucun `docker compose` n'a tourné.** Le déploiement, `ReadWritePaths`, le groupe `docker` : non vérifiés |
| Navigateur | Playwright 1.49, Chromium sans tête, 1280×800, français ([`parcours.mjs`](parcours/parcours.mjs)) | Le **temps est celui du script** : il ne lit pas, ne cherche pas, ne se trompe pas. C'est un **plancher**. Les « devinettes » et les pavés sont comptés à part |
| Clics | Comptés sur le chemin que prendrait un nouvel utilisateur | « Retours » : revenir en arrière parce que le premier endroit essayé ne menait nulle part |
| Secrets | Jetons d'enrôlement (`oc_…`) et URL de ping (`hb_…`) masqués avant chaque capture ; le mot de passe est une phrase de banc | — |

Les mesures brutes : [`parcours/mesures.jsonl`](parcours/mesures.jsonl).

### Ce que le guide demande avant de commencer

Le README et `docs/installation.md` ont été suivis sans autre aide. Deux
choses se sont vues dès le départ :

- `mkdir -p dev/state` (README, et `make run`) crée un dossier en `0755` ; le
  serveur écrit alors au journal `state_dir is open to the group or others:
  control socket not opened, export and apply disabled`. Le README ne le dit
  pas : `opencloud export` / `apply` et le bouton **Exporter** du
  [Services](#t4--déclarer-un-service) tombent sans que la cause se lise à l'écran.
- `docs/installation.md` écrit `opencloud-v0.5.0` dans la préparation de
  l'agent et dit que c'est « la version que `opencloud version` affiche » ;
  le binaire affiche `v0.0.1`, et l'écran de première configuration écrit
  `opencloud-v0.0.1`. Deux commandes différentes pour la même étape.

## Les tâches

Chaque table : **Temps** (script, plancher), **Clics**, **Retours**,
**Devinettes** (là où il a fallu deviner ou lire un pavé), **Attendu / trouvé**.

### T1 · Premier lancement

| | |
| --- | --- |
| Temps · clics · retours | 3,8 s · 2 · 0 |
| Chemin | ouvrir l'adresse → 3 champs → **Créer le compte** → écran « L'agent de cette machine » → **Ouvrir l'interface** |
| Attendu | Créer le compte, voir l'interface |
| Trouvé | Le compte se crée tout de suite. Puis un écran de 2 pavés de code (préparation, enrôlement) à jouer **en root sur la machine** ; l'interface n'est atteinte qu'après |
| Bloquants | L'unité systemd n'est pas donnée : « Puis activez l'unité `opencloud-agent.service` (voir la documentation d'installation) » : ni fichier, ni commande, ni lien |
| Devinettes | Une bannière **rouge** « L'adresse d'openCloud paraît locale… Renseignez public_url dans la configuration » : sur une installation locale voulue, c'est une erreur affichée ; elle ne dit pas dans quel fichier, ni qu'il faut redémarrer |
| Capture | [1](parcours/01-premier-lancement-1-compte.png) · [2](parcours/01-premier-lancement-2-agent.png) · [3](parcours/01-premier-lancement-3-vue-ensemble.png) |

### T2 · Ajouter une machine

| | |
| --- | --- |
| Temps · clics · retours | 4,4 s · 8 · 0 (avec 3 droits à cocher) |
| Chemin | Machines → **Ajouter une machine** → nom → **Avancé : droits de l'agent** → 3 cases → **Créer le jeton** → **Suivant** |
| Attendu | Une commande à coller, et le moyen de la jouer |
| Trouvé | Une fenêtre en 3 étapes ; la machine apparaît dans la liste « En attente d'enrôlement » avec son jeton tronqué et **Annuler le jeton** |
| Bloquants | La page d'installation ne suffit pas seule : l'unité systemd n'y est pas, le binaire `/tmp/opencloud` doit déjà être sur la machine (aucune consigne pour l'y mettre), le fichier `droits.conf` est à écrire à la main |
| Devinettes | 2 paragraphes de 53 mots ; le jeton s'affiche **deux fois** (champ « Jeton », puis dans la commande) ; les 7 lignes de préparation sont **coupées** par la boîte de code (défilement horizontal) ; les droits n'expliquent pas ce qu'ils exigent (Docker, `ReadWritePaths`) |
| Menus déroulants | Le « Rôle » s'ouvre devant la fenêtre : correct |
| Capture | [nommer](parcours/02-ajouter-machine-1-nommer.png) · [droits](parcours/02-ajouter-machine-2-droits.png) · [installer](parcours/02-ajouter-machine-3-installer.png) · [attente](parcours/02-ajouter-machine-4-attente.png) |

### T3 · Voir les conteneurs d'une machine

| | |
| --- | --- |
| Temps · clics · retours | 2,9 s · 4 · **1** |
| Chemin | tuile « Conteneurs » de la vue d'ensemble (**muette**) → retour → Machines → **Ouvrir** → onglet **Conteneurs** |
| Attendu | Cliquer le chiffre « Conteneurs » mène à la liste |
| Trouvé | 4 tuiles sur 5 ne mènent nulle part : Machines, Conteneurs, Disponibilité, Tâches planifiées. Seule « Alertes » est un lien |
| Devinettes | La colonne Image coupe au caractère (`grafana/gr` / `afana:11.2` / `.0`) ; le bandeau « En attente — La source n'a pas encore été lue pour ce système ; elle le sera à la prochaine passe » reste en tête de chaque onglet de la fiche |
| Capture | [tuile muette](parcours/03-conteneurs-0-tuile-muette.png) · [liste](parcours/03-conteneurs-1-liste.png) |

### T4 · Déclarer un service

Deux chemins mesurés.

| | **T4a · Accepter une proposition** | **T4 · Le formulaire** |
| --- | --- | --- |
| Temps · clics · retours | 2,6 s · 2 · 0 | 5,5 s · 7 · 0 |
| Chemin | Services → **Accepter** | Services → **Déclarer un service** → 4 étapes (Identité, Unités, Projets, Récapitulatif) → **Déclarer** |
| Trouvé | Le projet Compose `demo-shop` devient **1 service de 2 unités**, « Conforme » | Il faut retaper l'image et le nom du conteneur, que l'écran précédent affichait déjà |
| Devinettes | La pastille du menu dit **Services 6** (6 conteneurs sur 2 machines), et reste à 6 après l'acceptation | « Nature », « Origine », « Unités », « Projets » : quatre mots d'annuaire pour un conteneur. L'étape Projets se clôt par « Créez-en un depuis l'onglet Projets » : pas de lien, et l'assistant est à moitié rempli |
| Capture | [propositions](parcours/04-service-0-propositions.png) · [accepté](parcours/04-service-0b-accepte.png) | [1](parcours/04-service-1-identite.png) · [2](parcours/04-service-2-unites.png) · [3](parcours/04-service-3-projets.png) · [4](parcours/04-service-4-recapitulatif.png) |

Le même projet sur deux machines donne deux propositions, donc deux services
si on accepte les deux : voir [#437](#déjà-couvert).

### T5 · Surveiller un site (Disponibilité)

| | |
| --- | --- |
| Temps · clics · retours | 6,5 s · 4 · 0 |
| Chemin | vue d'ensemble → carte **Disponibilité › Configurer** → nom, adresse → **Suivant** → **Suivant** → **Créer la sonde** |
| Attendu | Un menu « Disponibilité » ou « Surveillance » |
| Trouvé | **Pas de menu** tant qu'aucune sonde n'existe ; seule la carte « À configurer » y mène. Après la première sonde, l'entrée apparaît avec sa pastille |
| Devinettes | L'étape « Rythme » demande 4 nombres (intervalle, délai, « Échecs avant Hors ligne », « Succès avant retour ») avant d'avoir une seule sonde ; les valeurs par défaut conviennent. Cible : 1 écran de 2 champs suffirait |
| Résultat | Sonde créée, « Nouveau », « dernier essai jamais » ; la mesure arrive à l'intervalle choisi (60 s) |
| Capture | [cible](parcours/05-sonde-1-cible.png) · [rythme](parcours/05-sonde-2-rythme.png) · [récap](parcours/05-sonde-3-recapitulatif.png) · [résultat](parcours/05-sonde-4-resultat.png) |

### T6 · Tâche planifiée

| | |
| --- | --- |
| Temps · clics · retours | 3,9 s · 3 · 0 |
| Chemin | carte **Tâches planifiées › Configurer** → nom, intervalle → **Suivant** → **Créer la tâche** |
| Trouvé | **Le meilleur écran de l'audit** : l'URL de ping, puis des extraits prêts à copier (un ping, une ligne de cron, un script avec code de sortie, un `HEALTHCHECK` Docker) |
| Devinettes | L'intervalle est en **minutes** (tâche) alors que la sonde est en **secondes** ; même bannière rouge « paraît locale » |
| Capture | [tâche](parcours/06-tache-1-tache.png) · [récap](parcours/06-tache-2-recapitulatif.png) · [ping](parcours/06-tache-3-ping.png) |

### T7 · Canal de notification, puis alerte

| | |
| --- | --- |
| Temps · clics · retours | 5,0 s · 6 · **1** (canal) ; 2,0 s · 2 · 0 (lire l'alerte) |
| Chemin | Alertes (pas de canal ici, **retour**) → Paramètres → **Ajouter un canal** → nom, URL → **Suivant** → **Suivant** → **Créer le canal** |
| Attendu | « Alertes → Canaux », comme le dit le journal (« Alertes › Canaux ») et `docs/installation.md` ligne 729 |
| Trouvé | Le canal vit sous **Paramètres → Notifications → Canaux**. Les deux textes sont faux |
| Alerte | Site arrêté à 13:06:32, alerte « Sonde hors ligne » vue à 13:10 au plus (≈ 3 min : 3 échecs × 60 s). Le webhook a reçu le JSON (fait, cause, action). La page dit le fait, la cause, l'action, et à quels canaux elle est partie |
| Devinettes | Fenêtre du canal : 63 mots sur la ligne « Format du corps » + aide ; la page d'alerte est claire |
| Capture | [alertes vide](parcours/07-alertes-0-page.png) · [canal 1](parcours/07-canal-1-destination.png) · [2](parcours/07-canal-2-envoi.png) · [créé](parcours/07-canal-3-cree.png) · [alerte](parcours/07-alerte-1-liste.png) · [détail](parcours/07-alerte-2-detail.png) |

### T8 · Plan de sauvegarde

| | |
| --- | --- |
| Temps · clics · retours | 6,8 s · 7 · **1** |
| Chemin | carte **Sauvegardes › Configurer** → **Ajouter un plan** (2 étapes, « Destination : Choisir… » vide) → retour → **Créer la clé** → **Ajouter la destination** |
| Attendu | Dire quoi sauvegarder, où, quand |
| Trouvé | L'assistant de plan avance sans clé ni destination et ne le dit qu'à l'étape 2. La liste des machines proposées comme destination contient **orca et vps-lyon, alors qu'aucune n'a le droit de recevoir** ; le refus tombe après saisie : « ré-enrôlez-le avec « recevoir des sauvegardes » sur ce chemin » (sans lien vers la machine) |
| Devinettes | Clé, destination, plans, inventaire, cibles sur une page : « où est la sauvegarde, de quoi, sur quelle machine » ne se lit nulle part d'un coup d'œil. **Refonte #366** |
| Capture | [page](parcours/08-sauvegardes-0-page.png) · [plan](parcours/08-sauvegardes-1-plan-destination.png) · [clé](parcours/08-sauvegardes-2-cle.png) · [après clé](parcours/08-sauvegardes-3-apres-cle.png) · [destination](parcours/08-sauvegardes-4-destination.png) |

### T9 · Pare-feu (arrêt à l'étape « Accès »)

| | |
| --- | --- |
| Temps · clics · retours | 2,2 s · 2 · 0 |
| Chemin | carte **Pare-feu › Configurer** → nom → **Suivant** (arrêt : adresse, clé, secret) |
| Trouvé | Une page d'explication **avant** le formulaire : 3 pavés (jusqu'à 39 mots) dont une liste de 6 privilèges OPNsense en ligne, à recopier à la main dans OPNsense |
| Devinettes | « Système › Accès › Utilisateurs » est du texte : il n'y a rien à cliquer ; le menu « Pare-feu » n'existe pas avant ce premier réglage |
| Capture | [page](parcours/09-parefeu-1-page.png) · [accès](parcours/09-parefeu-2-acces.png) |

### T10 · Accès SSH

| | |
| --- | --- |
| Temps · clics · retours | 1,7 s · 2 · 0 |
| Chemin | Machines → onglet **Accès SSH** |
| Trouvé | Le tableau « par clé » est lisible. En tête : « À regarder — 2 machines ne sont pas lues en entier » |
| Devinettes | Cet avertissement **s'affichera probablement sur toute installation à compte dédié** (l'agent ne lit que ses propres clés : le journal dit `unreadable: 2`) ; il ne dit pas quoi faire. Deux noms pour le même sujet : **Accès SSH** (Machines) et **Clés SSH** (fiche) |
| Capture | [accès](parcours/10-ssh-1-acces.png) |

### T11 · Journal

| | |
| --- | --- |
| Temps · clics · retours | 2,1 s · 2 · 0 |
| Trouvé | La table dit qui, quoi, quand. Mais la première ligne qu'un nouvel admin lit est « **Révocation — Jeton d'API** » (créer le compte révoque les jetons : il n'en a jamais créé) et « **Accès à l'interface interface** » (mot répété) |
| Devinettes | Un bandeau invite à cocher « Recevoir le journal » sur un canal « (Alertes › Canaux) » : chemin faux, texte non cliquable |
| Capture | [liste](parcours/11-journal-1-liste.png) · [détail](parcours/11-journal-2-detail.png) |

### T12 · Paramètres

| | |
| --- | --- |
| Temps · clics · retours | 2,5 s · 1 · 0 |
| Trouvé | Une page de 8 sections empilées (Accès, Modules, Notifications, E-mails, Fin de support, Agent IA, Jetons, À propos) avec une navigation latérale |
| Devinettes | 8 blocs de plus de 25 mots (jusqu'à 63) ; « Agent IA » parle de `mcp_url` dans `config.toml` et de redémarrage, sans dire où est ce fichier |
| Capture | [page](parcours/12-parametres-1-page.png) |

### T13 · Mettre à jour un agent

| | |
| --- | --- |
| Temps · clics · retours | 2,6 s · 3 · 0 pour **lire** ; la mise à jour elle-même n'a pas pu être menée (voir ci-dessous) |
| Chemin | Machines → **Ouvrir** → onglet **Agent** |
| Trouvé | « L'agent n'a pas le droit « mettre à jour l'agent » : **changez ses droits** » : une phrase, **aucun lien**. Le chemin réel : fiche → **Ré-enrôler** (fenêtre en 3 étapes) → coller la ligne sur la machine après avoir **arrêté le service et supprimé `identity.json`** |
| Bloquant | L'écran de première configuration n'installe que `observe,probe.lan` : **la machine openCloud ne se mettra jamais à jour depuis la fiche sans ré-enrôlement**. Et la `v0.0.1` n'embarque pas la clé des versions signées (`no_releases`, dit `docs/installation.md`) : le geste n'existe pas encore |
| Devinettes | « Retour arrière automatique — Non : cet agent n'a pas été migré (**voir ci-dessous**) » : rien dessous |
| Capture | [fiche](parcours/13-agent-1-fiche.png) · [ré-enrôler](parcours/13-agent-2-reenroler.png) |

## Les remarques de Lucas, vérifiées partout

| Remarque | Vérifié sur | Résultat |
| --- | --- | --- |
| **Menus déroulants derrière les fenêtres** | 39 listes, 21 écrans ou fenêtres (machine, service, sonde, tâche, canal, plan, pare-feu, type, « Faire taire », silence, jeton, destination, filtres des pages) | **Aucune masquée.** Deux listes de 13 entrées (type d'alerte) défilent dans leur cadre : 9 visibles, le reste par la molette. À noter : un menu long sans repère de défilement |
| **Textes d'aide en pavé** | 33 relevés d'écran (paragraphes de plus de 25 mots) | Pires : Paramètres (8 blocs, 63 mots), fenêtre du canal (63), installation (53), fiche Agent (45), Pare-feu (39). **Bons : tâche (T6), alerte (T7), assistants de sonde et de service** : un champ, une ligne |
| **Noms de menu exacts, liens directs** | 6 renvois « X › Y » dans `internal/lang/fr.toml` + `docs/installation.md` | Ce sont des chaînes du catalogue, rendues en texte (vérifié sur le Journal). 2 sont faux : « Alertes › Canaux » (réel : Paramètres → Notifications → Canaux) et « Paramètres › Jetons d'API » (réel : Paramètres → Agent IA → Jetons d'API) |
| **Sauvegardes incompréhensible** | T8 | Confirmé ; **refonte #366** |
| **Type de machine invisible** | Liste, fiche, vue d'ensemble | Visible (« VPS », « Type inconnu » + « Corriger le type ») : **F3** |
| **Logo collé au menu** | Toutes les pages | Détaché : **F3** |
| **« Service » faux** | Services, pastille du menu | Confirmé : 6 pour 2 projets de 2 conteneurs + 1 conteneur seul, sur 2 machines ; **#437** |

## Défauts classés

Pour chaque défaut : l'écran, la cause probable, **ce que verrait l'utilisateur
après**, les autres écrans touchés.

### Bloquants

**B1 · Un droit manquant se dit en phrase, jamais en geste**

| | |
| --- | --- |
| Écran | Fiche › Agent (« changez ses droits ») ; Sauvegardes › Ajouter la destination (« ré-enrôlez-le avec… ») ; fiche d'un hôte sans Docker |
| Cause probable | Le droit est une propriété de l'agent, posée à l'installation ; l'interface sait qu'il manque, mais n'a pas de geste qui y mène. Le texte vient de la couche du refus (`internal/refusal`), pas de l'écran |
| Proposition | Chaque refus de droit porte un bouton **Ré-enrôler avec « Mettre à jour l'agent »** qui ouvre la fenêtre déjà cochée. Le sélecteur de destination grise les machines sans le droit, avec la raison à côté. La fenêtre « Ré-enrôler » montre en tête les 3 gestes à faire sur la machine, numérotés |
| Autres écrans | Déployer un service (`docker.deploy`), écrire des fichiers, producteur Proxmox (`backup.produce`), sondes vers le réseau local (`probe.lan`) |
| Coût | Un bouton de plus par refus ; le coût de sécurité reste celui de ré-enrôler (jeton neuf) : **c'est à Lucas de dire si la mise à jour de l'agent se coche d'office** |

**B2 · L'installation de l'agent ne se fait pas depuis l'interface seule**

| | |
| --- | --- |
| Écran | Écran de première configuration ; fenêtre « Nouvelle machine » étape 2 |
| Cause probable | Le texte porte la préparation et la ligne d'enrôlement, mais renvoie à la documentation pour l'unité systemd, le fichier `droits.conf` et le binaire à poser |
| Proposition | L'étape 2 devient 5 étapes numérotées, **chacune avec un seul bloc à copier** : 1 le binaire (une commande `scp` ou `curl` prête, avec l'adresse du serveur), 2 la préparation, 3 l'enrôlement, 4 le fichier de l'unité (contenu affiché, bouton Copier), 5 `droits.conf` si un droit le demande. Le jeton s'affiche une seule fois, dans la commande. Les lignes longues passent à la ligne |
| Autres écrans | Fiche › Ré-enrôler (même page) ; première configuration (même texte, version locale) |
| Coût | Une page plus longue, mais chaque ligne se lit une fois et se copie ; moins de pavés |

### Gênants

**G1 · Les renvois de menu sont du texte, et deux sont faux**
Écran : journal (bandeau), erreur d'un jeton hors portée, pare-feu, Services › Projets, fiche › Ré-enrôler (6 occurrences dans `internal/lang/fr.toml`, plus `docs/installation.md:729`).
Cause : la phrase est écrite en dur dans le catalogue.
Après : un lien **Paramètres → Notifications → Canaux** qui mène au bon bloc (ancre), et un test qui échoue si la cible n'existe plus. Même défaut : docs.

**G2 · Tuiles muettes de la vue d'ensemble**
Écran : vue d'ensemble. Machines, Conteneurs, Disponibilité et Tâches planifiées ne mènent nulle part ; Alertes oui.
Cause : le composant tuile n'a un lien que pour les alertes.
Après : chaque tuile mène à sa liste (Conteneurs → liste filtrée par machine ou tous les conteneurs). Même défaut : aucun autre écran vu.

**G3 · Les modules n'ont pas de menu avant leur premier objet**
Écran : barre latérale. Disponibilité, Pare-feu, Sauvegardes, Tâches planifiées n'y sont pas ; la carte « À configurer » de la vue d'ensemble est la seule porte, et diminue à chaque configuration.
Après : le menu montre les 4 modules dès le début, avec un point « à configurer » ; la carte reste un raccourci.
Coût : un menu de 9 entrées au premier lancement, contre 5. À Lucas de trancher.
Même défaut : Paramètres › Modules (même « Configurer »).

**G4 · Déclarer un service re-demande ce qu'on sait déjà**
Écran : Services › Déclarer un service. 7 clics, 4 étapes, image et nom de conteneur à retaper ; l'étape « Projets » finit en impasse (« Créez-en un depuis l'onglet Projets »).
Cause : l'assistant part d'un formulaire vide, les propositions vivent à part.
Après : la première étape propose les **conteneurs détectés** (« Choisir un conteneur ») et préremplit tout ; l'étape Projets est facultative, avec **Créer un projet ici**.
Même défaut : « Modifier avant d'accepter » (même assistant).

**G5 · Une bannière rouge pour une situation prévue**
Écran : première configuration, étape installation d'une machine, tâche (URL de ping) : « L'adresse d'openCloud paraît locale… ».
Cause : l'adresse est jugée sur la requête, la même alerte pour un essai local et pour un VPS mal réglé.
Après : une note neutre, avec la ligne exacte à poser (`public_url = "https://…"` dans `/etc/opencloud/config.toml`) et « puis `systemctl restart opencloud` » ; rouge seulement si l'adresse de la page est publique.
Même défaut : [`docs/installation.md#public_url`](../../installation.md#public_url).

**G6 · Une sonde en 3 étapes pour une URL, des unités qui changent**
Écran : Disponibilité › Nouvelle sonde (3 étapes, 4 nombres de réglage) ; Tâches planifiées (minutes) contre sonde (secondes).
Après : un seul écran **nom + adresse + Créer**, un repli « Réglages » avec les valeurs par défaut lisibles (« toutes les minutes, 3 échecs avant l'alerte ») ; mêmes unités affichées partout (« toutes les 5 min »).
Même défaut : pare-feu, canal (3 étapes pour 3 champs).

**G7 · « À regarder » permanent sur les accès SSH**
Écran : Machines › Accès SSH. Avec un compte dédié, l'agent ne lit pas les autres comptes : l'avertissement est affiché par défaut sur toute installation conforme au guide.
Après : l'état « lu en partie » est normal, en gris, avec une ligne « Pour tout lire : droit X » ; « À regarder » est réservé à ce qui exige un geste.

**G8 · README : un dossier d'état en `0755`**
Écran : README « Démarrage rapide », `make run` : `mkdir -p dev/state` donne un dossier ouvert ; le serveur coupe `export` et `apply`.
Après : `install -d -m 0700 dev/state` dans le README et le Makefile.

**G9 · La page d'installation ne dit pas ce que chaque droit demande**
Écran : cases de la fenêtre « Nouvelle machine » et « Ré-enrôler » (« Déployer un service Docker »…).
Après : sous chaque case cochée, une ligne : « demande Docker ≥ 20.10, le dossier `/srv/workspace`, et `ReadWritePaths` : l'étape 5 les écrit ». 

### Finition

| # | Écran | Défaut | Après |
| --- | --- | --- | --- |
| F-1 | Conteneurs (fiche) | Colonne Image coupée au caractère | L'image sur une ligne, `title` pour la valeur complète |
| F-2 | Fiche machine | Bandeau « En attente — La source n'a pas encore été lue pour ce système » sur tous les onglets | Une ligne dans « Support du système » seulement : « lecture en cours » |
| F-3 | Journal | « Accès à l'interface interface » ; première ligne « Révocation Jeton d'API » inexpliquée | « Accès à l'interface » ; « Compte créé : les jetons d'API existants sont révoqués » |
| F-4 | Fiche › Agent | « (voir ci-dessous) » sans suite | Retirer la mention, ou montrer la section |
| F-5 | Install de l'agent | Le jeton apparaît deux fois | Une seule fois, dans la commande |
| F-6 | Sauvegardes | Après « Créer la clé », rien ne rappelle de noter la phrase | Un encart « Notez la phrase de passe maintenant » |
| F-7 | Machines | Un onglet « Accès SSH », un onglet « Clés SSH » | Un seul nom |
| F-8 | Listes déroulantes de 13 entrées | Pas de repère de défilement | Ombre ou barre visible |
| F-9 | `docs/installation.md` | `opencloud-v0.5.0` alors que le binaire dit `v0.0.1` ; « Alertes → Canaux » | Aligner avec la version affichée ; corriger le chemin |

## Déjà couvert

- **F1 (#434)** : menus déroulants dans les fenêtres, gestes hors ligne : vérifié, tenu.
- **F2 (#435)** : non revérifié ici.
- **F3 (#436)** : logo détaché de la barre latérale, type de machine visible (VPS, « Type inconnu » + « Corriger le type ») : vérifié.
- **#437 (étude services)** : pastille « Services 6 », unité « service » : constat confirmé, non repris.
- **#366 (refonte des sauvegardes)** : page Sauvegardes en cinq blocs : constat confirmé, non repris.

## Comparaison avec des outils reconnus

Le geste, pas le style.

| Tâche | Outil | Le geste | Ce que fait openCloud | Source |
| --- | --- | --- | --- | --- |
| Surveiller un site | Uptime Kuma | **Add New Monitor** : type HTTP(s), nom, URL, intervalle 60 s, tentatives 3 ; **Save**, la vérification démarre | 3 étapes, 4 réglages avant la première sonde (G6) | [Tencent Cloud](https://www.tencentcloud.com/techpedia/144018), [RamNode](https://ramnode.com/guides/series/monitoring/uptime-kuma) |
| Canal de notification | Uptime Kuma | Réglages → Notifications → **Setup Notification** ; « Default enabled » (pour les futures sondes), « Apply on all existing monitors », bouton **Test** dans la même fenêtre | Canal : « Tester » existe, mais **après** création, dans la liste ; pas de « appliquer à tout » | [Ultra.cc](https://docs.ultra.cc/applications/uptime-kuma) |
| Ajouter une machine | Portainer | Environments → **Add environment** → type → **Start Wizard** : la commande de l'agent est **affichée**, à copier ; nom, adresse:port ; **Connect** | Même geste ; mais l'unité, le binaire et `droits.conf` manquent à la page (B2) | [Portainer](https://docs.portainer.io/admin/environments/add/docker/agent) |
| Ajouter une machine | Netdata | **Add nodes** : une seule commande avec le jeton, qui détecte le système, installe et connecte ; **Regenerate token** sur tout écran qui la montre | Deux blocs (préparation, enrôlement) et la doc pour le reste | [Netdata](https://learn.netdata.cloud/docs/netdata-cloud/connect-agent) |
| Ajouter une machine | Komodo | Une commande `setup-periphery.py`, puis **New Server** ; en v2, une clé d'intégration créée dans Server settings, passée à l'installation | Même logique de jeton à usage unique, sans script d'installation unique | [Komodo](https://komo.do/docs/setup/connect-servers) |
| Ajouter un serveur | Coolify | Ajouter le serveur (IP, clé SSH) : Coolify **valide tout seul** (SSH, système, Docker) et installe ce qui manque | openCloud n'installe rien ; la validation vit dans « Attente de l'agent » | [Coolify](https://coolify.io/docs/knowledge-base/server/introduction) |
| Plan de sauvegarde | Proxmox VE | **Datacenter → Backup → Add** : une fenêtre : stockage, sélection (tous ou choisis), horaire ; rétention dans un onglet Avancé | Clé, destination, plans, inventaire, cibles sur une page (#366) | [Proxmox](https://pve.proxmox.com/wiki/Backup_and_Restore) |
| Projet Compose, plusieurs services | Dockge | **Une pile = un `compose.yaml`**, listée avec ses services ; les fichiers restent sur disque ; une pile hors Dockge s'y range puis **Scan Stacks Folder** | Une proposition par projet, un service par machine (#437) | [Dockge](https://github.com/louislam/dockge) |

Ce que ces outils partagent, et qu'openCloud n'a pas encore : **la commande ou le
formulaire arrive complet à l'écran** (Portainer, Netdata), **le test est dans la
fenêtre de création** (Uptime Kuma), **un seul écran pour le cas courant**
(Uptime Kuma, Proxmox).

## Les 10 changements qui rendent le plus

| # | Changement | Ce que voit l'utilisateur | Défaut |
| --- | --- | --- | --- |
| 1 | Un refus de droit porte son geste : **Ré-enrôler avec ce droit** | Un bouton à la place de « changez ses droits » | B1 |
| 2 | L'installation d'une machine en 5 étapes copiables (binaire, préparation, enrôlement, unité, droits) | Une page où chaque bloc se colle, sans documentation à côté | B2 |
| 3 | Les renvois de menu deviennent des liens, avec un test | « Paramètres → Notifications → Canaux » mène au bon bloc | G1 |
| 4 | Toutes les tuiles de la vue d'ensemble mènent à leur liste | Cliquer « Conteneurs » ouvre les conteneurs | G2 |
| 5 | Une sonde en un écran (nom, adresse, Créer) | 2 champs, 1 clic, réglages repliés | G6 |
| 6 | Le sélecteur de destination n'offre que les machines qui peuvent recevoir | Les autres sont grisées, avec la raison et un lien | B1 |
| 7 | « Déclarer un service » part des conteneurs détectés | Un choix dans une liste, plus de retapage | G4 |
| 8 | Une note neutre à la place de la bannière rouge « paraît locale », avec la ligne à poser | Pas d'alarme sur un essai local | G5 |
| 9 | Les modules visibles dans le menu dès le premier lancement | « Disponibilité » se trouve là où on la cherche (**décision de Lucas**) | G3 |
| 10 | « À regarder » réservé à ce qui exige un geste (SSH, bandeau « En attente ») | Plus d'avertissement permanent sur une installation conforme | G7, F-2 |

## Non vérifié

- **Aucun `docker compose` réel.** Pas de Docker sur la VM : le projet Compose
  de test à deux services est un faux démon. Déploiement, mise à jour d'un
  service, groupe `docker`, `ReadWritePaths`, `/srv/workspace` : non essayés.
- **Aucune installation en root.** Pas de `useradd`, de lanceur
  `opencloud-guard`, d'unité système, de Traefik, de TLS, de `-pin`.
- **La mise à jour d'un agent n'a pas été jouée** : pas de version signée à
  importer (`no_releases` sur `v0.0.1`), droit `agent.update` absent de
  `orca`, droits d'écriture coupés sur `vps-lyon` (artefact du banc).
- **Le temps est un plancher** (script, sans lecture ni hésitation). Aucun
  humain n'a passé le parcours : « devinette » et « pavé » sont comptés,
  jamais chronométrés.
- **Pare-feu : arrêt à l'étape « Accès »**, comme demandé ; l'API OPNsense
  n'a pas été jointe.
- **Une seule résolution** (1280×800, clair, français). Ni téléphone, ni mode
  sombre, ni anglais.
- **Proxmox, SMTP, Matrix, page de statut publique, mcp-maison** : pas visités.
- **La mesure des pavés** compte les paragraphes de plus de 25 mots ; elle
  ne juge pas leur clarté.
- **Comparaisons** : lues dans la documentation ou des guides cités, pas
  installées ; les outils évoluent (Komodo a changé de méthode de connexion
  en v2).

## Nettoyage

Fait en fin de session : les 8 services `systemd --user` du banc
(`oc-audit-*`), le dossier d'état, les configurations, les sockets, le site
et le récepteur de webhook sont arrêtés et effacés ; `bin/` et `web/node_modules`
(ignorés par git) sont ceux de `make build`. Voir le message de livraison.
