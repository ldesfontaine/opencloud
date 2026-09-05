# Héritage de your-cloud

Ce document décide ce qu'openCloud **garde** de your-cloud, ce qu'il garde **en
plus simple**, ce qui **change de forme**, ce qu'il **jette**. Il ne tranche
aucune question ouverte du registre : quand une idée en touche une, il le dit.

## 1. Ce qu'on garde tel quel

| Idée | Dans openCloud | Quand |
|---|---|---|
| Une action, une approbation ; l'irréversible se confirme deux fois | `05-execution.md`. Un lot = une approbation, N résultats | tout de suite |
| Le rapport appartient à la machine | Une ligne par étape dans journald et `/var/lib/opencloud/actions/<id>/`, un constat final. Le code de retour est une ligne parmi d'autres | tout de suite |
| Un refus nomme sa cause et le geste qui la lève | Vérifications avant la première écriture. L'interface cite la sortie de la machine, jamais une paraphrase | tout de suite |
| Une action qui peut couper son propre accès annonce sa reprise | `sshd`, pare-feu, WireGuard : le chemin de retour fait partie de la description de l'action | tout de suite |
| Point d'entrée unique, routes déclarées, refus par défaut | Traefik, un fragment par hôte virtuel sous `data/`, écrit atomiquement. Hôte inconnu = 404 | tout de suite |
| L'exposition est un objet nommé, retirable | Le domaine est un objet. Le supprimer retire fragment et certificat. Supprimer un service exige d'abord ses domaines | tout de suite |
| Vérifier par le chemin réel | Dernière étape de « créer un hôte virtuel » : requête HTTPS au nom déclaré via le Traefik local. Échec = action échouée, pas de retour arrière automatique | tout de suite |
| Le pilotage n'est pas dans le chemin des services | Tout remonte seul au démarrage ; éteindre openCloud n'éteint rien (`02-roles.md`) | tout de suite |
| L'identifiant fait autorité, pas le libellé | Identifiants bornés pour chemins, unités, comptes. Les libellés s'affichent, ne s'interprètent jamais | tout de suite |
| Préparer fermé, publier en dernier | Démarrer, vérifier en local, puis seulement écrire le fragment Traefik. Retrait en ordre inverse | plus tard, avec le déploiement |
| Le collage est un clavier | Coller un `docker run` ou un `compose` pré-remplit le formulaire. Le parseur liste ce qu'il écarte | plus tard |
| La mise à jour du produit n'est pas une action ordinaire | Jamais par la file d'actions. Sa forme reste à décider (`10-cycle-de-vie.md`) | plus tard |

## 2. Ce qu'on garde en plus simple

| Idée | V1 | openCloud |
|---|---|---|
| Plan montré, approuvé, appliqué à l'identique | Enveloppe signée, double implémentation | Une action = un script ou un playbook versionné + paramètres typés. L'écran montre les quatre attributs et le contenu dépliable. Son empreinte identifie ce qui est approuvé |
| Retour arrière approuvé avec le plan | Deux documents signés | Attribut « réversibilité » : l'action nomme son inverse, ou déclare qu'il n'existe pas |
| Idempotence visible | Dérive comparée à un état attendu | Rejouer ne change rien, et le dit. La dérive est **signalée, jamais corrigée** : la machine fait foi |
| L'ordre des effets est le contrat | Sept autorités | Vérifier, écrire, recharger, vérifier par le chemin réel, constater. Trois autorités : interface, exécuteur, machine |
| Point de non-retour | Bascule, gel d'écritures | Réservé à restaurer et redéployer : l'écran nomme le point après lequel revenir perd des données |
| Fraîcheur explicite | Trois dimensions | « Dernière remontée il y a X », un seuil, à côté de chaque donnée. Une alerte de dérive d'horloge |
| Diagnostic local en lecture seule | Binaire dédié | `systemctl`, journald, `/var/lib/opencloud/actions/<id>/`. L'interface donne la commande exacte |
| Observer et agir sont deux chemins | PKI mTLS maison | Collecteur sortant sous un compte système distinct de l'utilisateur SSH. Jamais d'ordre dans ce canal |
| Interface qui n'invente rien | Ni série, ni tableau de bord | Aucun score inventé ; source et âge à côté de chaque donnée. Les courbes vivent dans la pile standard |
| Découverte locale, jamais de scan | Écran « ports en écoute » | Machines déclarées à la main. Un service = son unité ou son conteneur, jamais son port. openCloud **nomme ce qu'il ne pilote pas** |
| Zéro flux par défaut | Carte des liens | Pare-feu d'hôte : SSH filtré, 80/443, port WireGuard. Chaque action dit quel flux elle ouvre |
| Cloisonnement local par compte | Table nftables par compte | Un utilisateur par service, un réseau `compose` par environnement. C'est ce qui rend « plusieurs environnements par machine » vrai (`03-modele.md`) |
| Identités écrasées au point d'entrée | Débit annoncé | `X-Forwarded-*` acceptés d'un frontal déclaré seulement, délais bornés (`06-reseau-et-certificats.md`) |
| Préflight des capacités | Structure de faits, replis | Liste fixe lue avant la première écriture : systemd, disque, ports 80/443, `/srv`, sudo. Ce qui manque est un refus nommé, jamais une adaptation |
| Une adresse IP n'est pas une identité | Quatre autorités cryptographiques | Le port filtré ne remplace jamais la clé. Aucun secret n'en dérive un autre (`08-securite-et-secrets.md`) |
| Le tag à l'écran, le digest dessous | Collecteur de mises à jour | Le digest est résolu au déploiement et écrit dans le rapport. `latest` refusé |
| Déclarer n'est pas déployer | Trois schémas hachés | Déclaration enregistrée sans effet ; le déploiement épingle une version numérotée |
| Sauvegarde et restauration prouvées | Échange atomique réversible | Écrasement refusé, digest rapporté, test de restauration périodique. Restaurer est irréversible et confirmé (`07-donnees-et-sauvegardes.md`) |
| Portail d'authentification devant un site | Deux populations | Un booléen par domaine : public, ou derrière l'authentification du proxy. Les comptes du portail ne sont jamais des comptes openCloud |
| Identité SSH par machine | Commande forcée générique | Une paire de clés par machine, la privée immobile sur la machine openCloud. Client borné : sans PTY, sans agent, sans X11. L'étendue du `sudo` reste ouverte (`11-decisions.md`) |
| Clé d'hôte relevée hors bande | Fiche séparée | L'empreinte est affichée à l'enrôlement et saisie par l'opérateur. `known_hosts` strict. Changer adresse, port, compte ou empreinte est une action nommée |
| Installation sans préparation | Lecture fine de sudo | Compte admin ordinaire ; root direct accepté, jamais exigé. openCloud ne retire jamais un droit à l'opérateur |
| Audit avant proposition | Moteur de placement | Lire la machine, lister les modifications système, faire approuver avant tout |
| Le privilège n'existe que le temps de l'action | Mode « observation seule » | Aucun démon root maison |
| Le paquet ne fait rien | `.deb` inactif | Le programme posé ne démarre rien seul ; la première connexion fait tout. Unité durcie |
| Retrait propre, état partiel montré | Registre de démontage | La liste « modifications appliquées » de la vue Machine, relue sur la machine avant retrait, en ordre inverse |
| État durable atomique, chemins fixes | Fichiers d'autorité | Base en WAL ; tout fichier posé = temporaire + `fsync` + `rename` ; clés et `known_hosts` sous `/var/lib/opencloud`, jamais déplacés par argument |

## 3. Ce qui change de forme

Quatre bases ont changé : **web**, **une infrastructure**, **pas d'agent**,
**modèle impératif**.

| Idée | Devient | Quand |
|---|---|---|
| Pas de commande libre | Le catalogue est la liste fermée. Paramètres à schéma strict ; chemins dérivés du nom, jamais saisis. Une route par action, pas de route « exécuter » | tout de suite |
| Anti-rejeu, une commande à la fois | La file par machine sérialise. Identifiant unique + unité `oc-action-<id>` : redéposer le même échoue | tout de suite |
| Coupure = résultat inconnu | « Inconnu » devient transitoire : relecture de l'unité et de journald au retour. États fermés : en attente, en cours, terminée, échouée, à relire, injoignable. Relancer est un clic humain | tout de suite |
| Collecteurs nommés, minimaux | Service standard non-root, sources fermées : métriques système, journaux Traefik, état des unités. Ni contenu, ni variables, ni voisins réseau | tout de suite |
| Le produit constate le socle, ne le configure pas | Vue Domaines : attendu, observé, écart. **Deux écritures hors machine, nommées** : le TXT `_acme-challenge`, et sur demande l'enregistrement A/CNAME, chez Cloudflare | tout de suite |
| Machine sans adresse publique | WireGuard monté depuis la machine vers la machine openCloud, pour le pilotage, pas pour publier. Deux machines d'un même réseau local se parlent en direct | tout de suite |
| DNS-01, générique, page certificats | Gardé. Le jeton unique pose le challenge pour chacune. Comment le certificat arrive sur la machine : question ajoutée au registre | tout de suite |
| Secrets nés sur la machine | Le `.env` sous `data/`, complété sur place pour ce qui manque. Aucun secret dans un paramètre, un rapport, journald | tout de suite |
| Accès personnel prêté puis rendu | L'enrôlement crée l'utilisateur openCloud, sa clé, sa règle `sudo`, et affiche l'empreinte d'hôte. Aucun secret personnel dans le navigateur. Le geste exact reste ouvert (`11-decisions.md`) | tout de suite |
| Le démarrage est une frontière | Un programme, un rôle ; configuration et base validées avant l'ouverture du port | tout de suite |
| Le nom publié, vu du dedans | Un client du réseau local qui joint son service par le nom public sort et revient : lent, panne silencieuse. La vue Domaines nomme la résolution interne attendue, ne la configure pas | tout de suite |
| Ce que le proxy ne publie pas | Hôte virtuel = HTTP seulement. Un port brut (courrier, base, jeu) est un flux nommé par action, ou un refus (`06-reseau-et-certificats.md`) | tout de suite |
| Lacune visible | La pile standard montre les trous. Une liste coupée dit combien il manque | plus tard |
| Tampon local borné | Âge, taille, nombre : trois réglages de l'expéditeur standard | plus tard |
| Réseau d'accès opérateur | WireGuard noyau sur la machine openCloud, sous-réseau fixe. Le tunnel n'authentifie pas : la session reste exigée | plus tard |
| Conteneur déclaratif | `compose` plutôt que Quadlet. Volumes sous `data/`. Rootless = option | plus tard |
| Lot embarqué, ancre scellée | Chaque outil posé est épinglé par version et somme, vérifié avant installation. Jamais de `curl \| sh` | plus tard |
| Remplacement explicite du pilote | « Réinstallation ailleurs » (`10-cycle-de-vie.md`) : restaurer `/var/lib/opencloud`, vérifier que la nouvelle machine joint chacune, retirer l'ancienne. Jamais déclenché par une panne | plus tard |

## 4. Ce qu'on jette

| Idée | Pourquoi |
|---|---|
| Fenêtre native de consentement | Se méfiait de la WebView. En web, afficher et approuver sont la même surface |
| Protocole d'observation maison | Aucun protocole n'est écrit ; les collecteurs sont standard |
| Déclaré contre vérifié, recette et reprise | Deux inventaires pour un existant qu'openCloud ne reprend pas |
| Profil d'accès cercle / accès / charge | Un booléen par domaine suffit |
| Coffre local, phrase, code de récupération | Plus de poste à protéger. Clé dans le gestionnaire de mots de passe |
| Appairage d'appareil | Plus d'appareil. Identifiant et mot de passe |
| Une application, plusieurs infrastructures | Une infrastructure, une instance |
| Sonde de validation jetable | Le préflight lit ; la pose de Traefik est la vraie preuve. Un bouton « tester l'accès » suffit |
| Autorité adaptée à chaque cible | Une seule sorte de cible : une machine, par SSH. Seule exception : l'API Cloudflare |
| Budgets par rôle, harnais à 64 machines | Une infrastructure. Des bornes sur l'unité, mesurées sur une petite VM |
| Découpage décider / agir en vingt modules | Un seul témoin : déposer dans une file exige une approbation enregistrée |
| Glossaire à part | Le vocabulaire vit dans `03-modele.md` |
| Renommer tout avant le premier utilisateur | Leçon seulement : fixer `opencloud`, `oc-action-`, `/var/lib/opencloud` avant le premier commit |

## 5. Table rase : la méthode de développement et de preuve

Harnais de preuve, contrats par incrément, bornes mesurées, grilles de
sécurité, CI à quinze contrôles, signatures, découpage du cadrage en étapes numérotées :
**rien n'est repris.** La méthode de développement d'openCloud se décidera au
moment du développement, pas avant.

## 6. Ce que your-cloud avait compris — gardé, en plus souple

Des intentions fortes, pas des verrous : openCloud automatise, tout reste
faisable à la main (`01-perimetre.md`).

- Rien ne s'applique en silence : montré avant, constaté après, jamais paraphrasé.
- La machine fait foi ; un plan parti n'est pas un plan appliqué.
- Un refus nomme sa cause et le geste qui la lève, avant tout effet.
- Observer et agir sont deux chemins ; compromettre l'un ne donne pas l'autre.
- Rien n'est promis avant d'être prouvé sur une vraie machine.

## 7. Ce que ce document a changé ailleurs

- `02-roles.md`, `10-cycle-de-vie.md` : « ré-adoption » devient « ré-enrôlement ».
- `06-reseau-et-certificats.md` : les écritures DNS sont nommées et au nombre de
  deux ; le nom publié vu du réseau local ; hôte virtuel = HTTP seulement.
- `11-decisions.md` : cinq questions ajoutées, et deux existantes précisées.
