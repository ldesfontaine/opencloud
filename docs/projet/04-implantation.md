# La norme d'implantation des machines

## Ce n'est pas une invention d'openCloud

Cette arborescence est **la norme de toutes les machines** — VPS, VM, serveurs
loués, machines au grenier — **qu'openCloud y soit installé ou non**. Beaucoup
n'auront jamais openCloud : juste des services posés à la main. Elles suivent
la même norme.

> **openCloud ne définit pas cette norme, il s'y conforme.** C'est un invité qui
> range au même endroit que tout le monde.

Trois conséquences, et elles sont utiles :

- **Une machine reprise plus tard par openCloud est déjà rangée** là où il
  regarde. Rien à convertir.
- **Une machine qui perd openCloud reste rangée**, lisible et utilisable à la
  main. La norme ne dépend pas de l'outil.
- **Rien ne « maintient » la norme** : un répertoire ne dérive pas. Elle n'a
  besoin d'aucun processus local pour rester vraie — ce n'est donc pas un
  argument pour un agent (voir `05-execution.md`).

**Rien sous `/srv` n'appartient à openCloud.** Son état — base, clés, jeton,
journal des actions — vit dans `/var/lib/opencloud/`, l'emplacement standard
pour l'état d'un programme. Il est sauvegardé par une règle nommée, en plus de
`/srv/data` (`07-donnees-et-sauvegardes.md`).

## Le découpage

```
<racine>/                        # /srv
├── workspace/                   # ce qui tourne — définitions, code, configuration
│   └── <environnement>/
│       └── <service>/
└── data/                        # ce qui doit survivre à un redéploiement
    ├── <environnement>/
    │   └── <service>/           # volumes, bases, fichiers applicatifs
    ├── backups/                 # sauvegardes et instantanés
    └── acme/                    # certificats et clés privées TLS
```

> **On peut effacer `workspace` et le reconstruire ; on ne peut jamais effacer
> `data`.** Cette frontière doit rester vraie sans exception, sinon elle ne sert
> à rien.

C'est elle qui décide où vit le `.env` d'un service : sous `data/`, jamais sous
`workspace/` (voir `08-securite-et-secrets.md`).

## Le programme, lui, va dans `/opt`

| | Définition | Ce qu'on y met |
|---|---|---|
| **`/opt`** | Logiciel applicatif ajouté à la machine, auto-contenu, non fourni par la distribution | **Le programme openCloud** : binaire, ressources embarquées |
| **`/srv`** | Les **données des services servis par cette machine** | **Ce que la machine sert** : services, données applicatives, sauvegardes |

L'usage courant brouille la frontière — beaucoup de gens posent des piles
Docker entières dans `/opt`, et ce n'est pas faux. Mais la lecture stricte est
plus utile : **`/opt` décrit le logiciel installé, `/srv` décrit ce qu'il
sert.** openCloud est les deux à la fois, donc il utilise les deux.

S'il est un jour distribué en paquet (`.deb`), il suit la convention du paquet
— `/usr/bin`, `/etc/opencloud` — et `/opt` disparaît. Ce que la machine
**sert** ne bouge pas pour autant.

## Quand `/srv` contient déjà autre chose

**La norme prime toujours.** openCloud utilise `/srv/workspace` et `/srv/data`
quoi qu'il trouve à côté. Il crée ce qui manque et ne touche à rien d'autre.
Aucune racine variable, aucun repli.

Pas d'inventaire de l'existant, pas d'adaptation. Une machine déjà tenue par un
autre gestionnaire est un **refus**, pas un cas à absorber.

**Le refus nomme ce qu'il a trouvé** — « port pris » ne suffit pas :

| Ce que le préflight cherche | Pourquoi ça arrête tout |
|---|---|
| Un gestionnaire présent — **Plesk**, **cPanel** | Il tient déjà le proxy, le pare-feu et les certificats |
| Un **`certbot` actif** | Deux clients ACME sur le même domaine se disputent le renouvellement |
| Le **processus qui tient le port 80 ou 443** | Le conflit le plus direct, et le plus visible |

openCloud ne s'y adapte pas : il échoue explicitement, et il dit **lequel des
trois** l'a arrêté (`15-catalogue-actions.md` §5 et §6).

**Les journaux des services vivent dans `journald`**, pas sous `/srv`. Rotation
par le système, expédition par le collecteur ; rien à sauvegarder.

## Ce qui ne vit pas sous `/srv`

Ce qu'openCloud configure au niveau du système n'y vit pas et ne peut pas y
vivre : pare-feu, unités systemd, paquets, utilisateurs, `sshd`. La règle
complète est donc :

> `/srv` contient ce que la machine **sert** ; le reste du système ne subit que
> des modifications **nommées, listées et réversibles**.

Chacune de ces modifications se propose, se montre, et ne s'applique jamais en
silence (voir `08-securite-et-secrets.md`).
