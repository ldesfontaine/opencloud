# La pile technique

**Décidé : Go, interface rendue côté serveur, un seul binaire.** Ce document garde les options comparées et la raison du choix.

## Ce que le produit demande vraiment

Avant de comparer des langages, la liste de ce qu'il faut savoir faire :

| Besoin | Pourquoi c'est structurant |
|---|---|
| **S'installer sur une machine quelconque** | openCloud se pose dans `/opt` sur une machine qui sert déjà. Tout ce qu'il exige en plus (runtime, serveur web, gestionnaire de paquets) est une chose à installer, à maintenir et à mettre à jour sur la machine qu'on voulait garder propre |
| **Parler SSH**, longuement | Chaque action est un processus distant qui dure des minutes |
| **Diffuser une sortie en direct** | Sans flux au fil de l'eau, un blocage n'est pas détectable (`05-execution.md`) |
| **Tenir une file par machine** | Un exécuteur par machine, en série, qui survit aux redémarrages |
| **Un peu d'état** | Une petite base — SQLite suffit à l'échelle d'une infrastructure |
| **Une interface qui montre avant d'agir** | Beaucoup de tableaux, des formulaires, des journaux qui défilent |

## Les options

| | Pile | Ce qu'elle donne | Ce qu'elle coûte |
|---|---|---|---|
| **A** | **Go** + interface embarquée | **Un seul binaire**, sans runtime : installer, c'est copier un fichier et poser une unité systemd. SSH, exécution concurrente et flux en direct sont natifs. SQLite sans dépendance | Deux langages si l'interface est une application à part ; plus de code qu'un cadre pour les écrans ordinaires |
| **B** | **Laravel** (PHP) | L'interface la plus rapide à écrire, un cadre que tu connais déjà, files et tâches planifiées fournies | **Exige PHP-FPM, Composer et un serveur web sur la machine openCloud.** Et openCloud gère justement les versions de PHP des sites (`03-modele.md`) : sa propre dépendance s'emmêle avec ce qu'il administre |
| **C** | **Node / TypeScript** | Un seul langage du serveur au navigateur, bon en flux | Un runtime et un `node_modules` à maintenir sur le serveur |
| **D** | **Python** (FastAPI) | Le choix naturel **si Ansible est retenu** — on pilote Ansible par sa bibliothèque au lieu de l'appeler en ligne de commande | Le plus pénible à empaqueter proprement sur un serveur |

## Recommandation

**Go pour le service, interface rendue côté serveur au départ.**

Trois raisons, dans l'ordre :

1. **L'installation.** Un binaire et une unité systemd. C'est décisif pour un
   outil dont la promesse est de se poser sans déranger la machine, et ça colle
   à « openCloud crée ses répertoires et rien de plus » (`04-implantation.md`).
2. **Le modèle d'exécution colle.** Un exécuteur par machine, SSH natif, sortie
   diffusée en direct : c'est exactement la partie difficile du produit, et
   c'est là que Go demande le moins d'efforts.
3. **Pas d'emmêlement de runtime.** openCloud administre des versions de PHP ;
   il vaut mieux qu'il ne dépende pas de PHP pour tourner.

**Pour l'interface**, la première étape annoncée est « un front minimal, un
bouton s'il le faut » (`12-methode.md`). Deux chemins :

- **Rendu côté serveur** — gabarits Go et HTMX, avec les journaux en direct
  par un flux d'événements. **Aucune chaîne de compilation front, aucun
  `node_modules`**, et ça couvre les tableaux, les formulaires et les journaux
  qui défilent, c'est-à-dire l'essentiel de cet outil.
- **Application à part** — React, Vue ou Svelte, empaquetée dans le binaire.
  Plus confortable si l'interface devient riche, au prix d'une chaîne de
  compilation et d'un second langage dès le premier jour.

> *Proposition* : commencer par le rendu côté serveur, et n'ajouter une
> application front que sur les écrans qui la réclament vraiment. Dans les deux
> cas l'interface finit **embarquée dans le binaire**, donc l'installation ne
> change pas.

## L'objection honnête

**Laravel donnerait une interface utilisable plus vite**, et pour un projet
mené seul ce n'est pas un argument mineur. Le coût ne se paie pas à l'écriture,
il se paie à l'installation et à l'exploitation : un runtime PHP à tenir à jour
sur la machine qui doit rester la plus fiable de l'infrastructure.

Si la vitesse de construction de l'interface devient la contrainte qui décide,
c'est un choix défendable — à condition de le faire les yeux ouverts.

## Lien avec le choix des actions

Le choix **Ansible / scripts / SSH direct** (`11-decisions.md`) et celui de la
pile ne sont pas indépendants : **si Ansible est retenu, Python remonte** dans
le classement. Trancher la question n° 1 d'abord, ou au moins en même temps.
