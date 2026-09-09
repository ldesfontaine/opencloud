# Réduire le pouvoir du jeton DNS — étude du 9 septembre 2026

**Cette étude ne décide rien.** Elle prépare la question ouverte du registre
(`11-decisions.md`, partie 2) et le ticket #6. Ni `06-reseau-et-certificats.md`
ni le registre ne sont modifiés.

## La question

Chaque machine obtient seule son certificat en DNS-01 : le jeton Cloudflare de
la zone est donc copié sur chaque machine qui sert la zone. Ce jeton peut
réécrire **tout** le DNS de la zone, pas seulement `_acme-challenge`. Existe-t-il
une voie qui garde « Traefik fait tout, sur sa machine, même la machine
openCloud éteinte » **sans** qu'une machine compromise puisse détourner la zone ?

## Deux dangers à séparer

Ils ne se traitent pas ensemble, et les confondre fait croire qu'une piste
protège plus qu'elle ne protège.

| Danger | Ce qu'il permet | Ce qui l'arrête |
|---|---|---|
| **Détourner le trafic** | Réécrire les A/CNAME/MX/CAA de la zone : le trafic va ailleurs | Que le jeton n'ait **aucun** droit sur la zone du domaine |
| **Certifier au nom d'un autre** | Répondre au challenge d'un nom qui n'est pas le sien, obtenir un certificat valide | Que chaque machine n'écrive que **son propre** enregistrement de challenge |

Le statu quo ne bloque ni l'un ni l'autre. **La délégation seule bloque le
premier.** Seule la délégation **avec un droit d'écriture par nom** bloque les
deux.

## Tableau comparatif

| Piste | Protège quoi | Coûte quoi | Traefik tel quel | Wildcard | Survit à l'extinction de la machine openCloud |
|---|---|---|---|---|---|
| **1. Statu quo** — jeton de zone copié | Rien de plus que la séparation entre zones | Rien | Oui | Oui | Oui |
| **1 bis. + filtrage IP et TTL** | Le **fichier volé** réutilisé ailleurs | Un jeton par machine créé à la main, à refaire à chaque changement d'IP et à chaque expiration | Oui | Oui | Oui |
| **2a. Délégation vers une 2ᵉ zone Cloudflare** | Le détournement du trafic | Un **second nom de domaine** acheté (une sous-zone `acme.exemple.net` est réservée à l'offre Enterprise) + un CNAME par nom certifié | Oui | Oui | Oui |
| **2b. Délégation vers acme-dns auto-hébergé** | Le détournement **et** la certification au nom d'un autre | Un service DNS de plus, sur une machine publique toujours allumée, port 53 + un CNAME par nom certifié + un aller-retour obligatoire avant le 1ᵉʳ certificat | Oui (fournisseur `acmedns`) | Oui | Oui, **si** acme-dns ne tourne pas sur la machine openCloud |
| **2c. Délégation vers deSEC** | Le détournement **et** la certification au nom d'un autre | Un opérateur DNS de plus dans la chaîne, gratuit donc sans engagement + un CNAME par nom certifié + une 2ᵉ API à écrire dans openCloud | Oui (fournisseur `desec`) | Oui | Oui |
| **3. Une zone Cloudflare par machine** | Le détournement des **autres** zones | Un nom de domaine acheté par machine ; incompatible avec « un seul domaine, l'environnement est dans le chemin » | Oui | Oui | Oui |
| **4. HTTP-01 sur les machines publiques** | Tout : **aucun jeton sur la machine** | Pas de wildcard, port 80 joignable obligatoire, machines derrière NAT exclues, deux mécanismes à tenir | Oui (`httpChallenge`) | **Non** | Oui |

Deux contraintes de Traefik pèsent sur tout le tableau :

- **Un seul fournisseur DNS par Traefik.** « Multiple DNS challenge provider are
  not supported with Traefik, but you can use CNAME to handle that »
  (<https://doc.traefik.io/traefik/reference/install-configuration/tls/certificate-resolvers/acme/>).
  Une machine ne mélange donc pas Cloudflare et acme-dns : le passage d'une piste
  à l'autre se fait **machine par machine, en entier**.
- **Le wildcard impose DNS-01.** « wildcard certificates can only be generated
  through a DNS-01 challenge » (même page).

## 1. Le statu quo — jeton de zone copié

### Ce que `Zone / DNS / Edit` couvre exactement

| Fait | Source |
|---|---|
| `Edit` est le CRUDL complet : « `Edit` is full CRUDL (create, read, update, delete, list) access » | <https://developers.cloudflare.com/fundamentals/api/get-started/create-token/> |
| La ressource la plus fine est **la zone** : « granting `Zone DNS Read` access to a zone `example.com` will allow the token to read DNS records only for that specific zone » | même page |
| Les permissions DNS appartiennent à la catégorie Zone ; aucune ressource plus fine qu'une zone n'est documentée | <https://developers.cloudflare.com/fundamentals/api/reference/permissions/> |
| lego demande « Zone / Zone / Read » et « Zone / DNS / Edit » | <https://go-acme.github.io/lego/dns/cloudflare/> |

Conséquence directe : **il n'existe pas de jeton Cloudflare limité à
`_acme-challenge`**. Le jeton qui pose le TXT peut aussi réécrire les A, les
CNAME, les MX et le CAA. C'est exactement la crainte de Lucas, et elle est
fondée.

lego permet de **couper le jeton en deux** — `CF_ZONE_API_TOKEN` (Zone:Read) et
`CF_DNS_API_TOKEN` (DNS:Edit) — « mainly interesting for users who manage many
zones/domains with a single Cloudflare account »
(<https://go-acme.github.io/lego/dns/cloudflare/>). **Ça ne protège rien ici** :
le jeton dangereux est le second, et il reste entier sur la machine.

### Les deux restrictions gratuites chez Cloudflare

| Restriction | Ce qu'elle fait | Source |
|---|---|---|
| **Client IP Address Filtering** | Limite les adresses d'origine autorisées, en CIDR. Ne s'applique **pas** au point d'appel *Verify Token* | <https://developers.cloudflare.com/fundamentals/api/how-to/restrict-tokens/> |
| **TTL** | `not_before` et `expires_on`. Par défaut « tokens don't expire and are long lived » | même page ; <https://developers.cloudflare.com/fundamentals/api/how-to/create-via-api/> |

**Ce qu'elles valent, dit franchement.** Le filtrage par IP protège du jeton
*exfiltré* : un fichier récupéré dans une sauvegarde ne sert plus ailleurs. Il ne
protège **pas** de la crainte de Lucas : un attaquant qui tient la machine parle
depuis l'adresse autorisée. Le TTL, lui, transforme le renouvellement de
certificat en corvée d'agenda, et il n'y a aucun moyen sûr de le renouveler
automatiquement (voir « écartées », jetons courts).

Coût réel : **un jeton par machine créé à la main** chez Cloudflare, refait à
chaque changement d'adresse publique. Les machines derrière NAT sortent par
l'adresse de leur box, qui bouge.

### Le budget Let's Encrypt, pour mémoire

Un attaquant qui certifie à tort consomme aussi le budget. Les chiffres qui
encadrent toutes les pistes (<https://letsencrypt.org/docs/rate-limits/>) :
50 certificats par domaine enregistré / 7 jours ; 5 certificats identiques /
7 jours ; **5 échecs d'autorisation par nom et par compte / heure** ; 300 nouvelles
commandes par compte / 3 heures.

## 2. Déléguer `_acme-challenge` par CNAME

### Le principe, et ce qu'il vaut

Let's Encrypt suit la délégation : « you can use CNAME records or NS records to
delegate answering the challenge to other DNS zones »
(<https://letsencrypt.org/docs/challenge-types/>). Traefik et lego suivent le
CNAME **par défaut** : « CNAME are supported and even encouraged », et « If
needed, CNAME support can be turned off with the following environment variable:
`LEGO_DISABLE_CNAME_SUPPORT=true` »
(<https://doc.traefik.io/traefik/reference/install-configuration/tls/certificate-resolvers/acme/>).

On pose donc, **une fois par nom certifié**, dans la zone du domaine :

```
_acme-challenge.site.exemple.com.  CNAME  site.zone-de-challenge.
```

La machine n'a plus aucun droit sur `exemple.com`. **Le détournement du trafic
est mort.** Le CNAME, lui, est un enregistrement de plus à créer avant le
premier certificat, et il ne s'efface jamais.

> **Le piège à ne pas rater.** Déléguer ne suffit pas à empêcher la
> certification au nom d'un autre. Si toutes les machines partagent un jeton sur
> la zone de challenge, une machine compromise réécrit le TXT du voisin et
> obtient un certificat valide pour son nom. Il faut un droit d'écriture **par
> nom**, ce que Cloudflare ne sait pas faire (§1).

### 2a. Une seconde zone Cloudflare

**Une sous-zone `acme.exemple.net` n'est pas possible sur les offres courantes** :
le « subdomain setup » est réservé à Enterprise — disponibilité « No / No / No /
Yes » pour Free, Pro, Business, Enterprise
(<https://developers.cloudflare.com/dns/zone-setups/subdomain-setup/>).

Il faut donc **acheter un second nom de domaine** et l'ajouter comme zone
normale. Bilan :

| | |
|---|---|
| Protège | Le détournement du trafic de `exemple.com` |
| Ne protège pas | La certification au nom d'un autre — le jeton de la zone de challenge est toujours à droits pleins sur elle |
| Coûte | Un domaine par an, un CNAME par nom certifié, le même travail de rotation |

**Peu rentable** : on achète un domaine pour ne régler que la moitié du
problème.

### 2b. acme-dns auto-hébergé

acme-dns est un serveur DNS minimal qui ne sert que les TXT de challenge. Un
compte par nom, et **le compte n'écrit que son enregistrement**.

| Fait | Source |
|---|---|
| Fournisseur lego `acmedns` ; `ACME_DNS_API_BASE`, `ACME_DNS_STORAGE_PATH`, `ACME_DNS_STORAGE_BASE_URL`, `ACME_DNS_ALLOWLIST` ; toute variable acceptée avec le suffixe `_FILE` | <https://go-acme.github.io/lego/dns/acmedns/> |
| « Limit /update API endpoint access to specific CIDR mask(s), defined in the /register request » — le filtrage par IP existe **par compte** | <https://github.com/acme-dns/acme-dns> |
| « Rolling update of two TXT records » : deux valeurs gardées, ce qui couvre `exemple.com` **et** `*.exemple.com` en même temps | même source |
| « needs to open a privileged port (53, domain), so it needs to be run with elevated privileges » ; délégation NS + A vers l'instance, publiquement joignable | même source |
| Instance publique `auth.acme-dns.io` : « You are encouraged to run your own acme-dns instance, because you are effectively authorizing the acme-dns server to act on your behalf » ; elle a été **injoignable en juin 2025** | même source ; <https://github.com/joohoi/acme-dns/issues/385> |
| Projet vivant, passé sous l'organisation `acme-dns/acme-dns` : **v2.0.2 le 5 février 2026**, dépôt non archivé | API GitHub, `acme-dns/acme-dns` |

**Le geste imposé au premier certificat.** lego enregistre le compte puis
**échoue exprès** en dictant le CNAME à créer :

> `acme-dns: new account created for %q. To complete setup for %q you must
> provision the following CNAME in your DNS zone and re-run this provider when it
> is in place`
> (<https://github.com/go-acme/lego/blob/master/providers/dns/acmedns/acmedns.go>)

openCloud devrait donc gérer un **aller-retour** : premier essai qui échoue,
lecture du CNAME dicté, écriture du CNAME chez Cloudflare (une action du
catalogue existe déjà), second essai. C'est faisable, et ce n'est pas gratuit.

**Où tourne acme-dns ?** Pas sur la machine openCloud : si elle s'éteint, plus
aucun renouvellement n'aboutit nulle part, ce que le projet refuse
(`02-roles.md`). Il faut donc une machine **avec adresse publique et toujours
allumée**, qui devient le point de défaillance unique de tous les
renouvellements — pas du trafic. Sur une petite infrastructure, c'est une
demande lourde.

### 2c. deSEC comme zone de challenge

deSEC est un hébergeur DNS qui sait **ce que Cloudflare ne sait pas** : un jeton
restreint à un seul nom et à un seul type d'enregistrement.

| Fait | Source |
|---|---|
| Politiques de jeton : `domain`, `subname`, `type`, `perm_write` — donc `{subname: "site1", type: "TXT", perm_write: true}` | <https://desec.readthedocs.io/en/latest/auth/tokens.html> |
| Aussi `allowed_subnets`, `max_age`, `max_unused_period` : filtrage IP et expiration, par jeton | même page |
| Fournisseur lego `desec`, `DESEC_TOKEN`, depuis la v3.7.0, suffixe `_FILE` accepté | <https://go-acme.github.io/lego/dns/desec/> |
| Un domaine à soi peut être hébergé (NS délégués à deSEC) ; les noms sous `dedyn.io` sont « limited to one per account » | <https://desec.readthedocs.io/en/latest/dns/domains.html> |
| Écritures limitées à `2/s 15/min 100/h 300/day` **par domaine** | <https://desec.readthedocs.io/en/latest/rate-limits.html> |

C'est la seule piste qui donne la protection complète **sans serveur à tenir**.
Le prix : un opérateur de plus dans la chaîne des renouvellements, un service
gratuit donc sans engagement de disponibilité, et une deuxième API DNS à écrire
dans openCloud pour créer les jetons par machine. Les 300 écritures par jour et
par domaine tiennent largement à l'échelle visée (un renouvellement = deux
écritures), mais c'est un plafond à connaître.

### Le complément qui ne coûte rien : CAA

Let's Encrypt honore les extensions de la RFC 8657
(<https://letsencrypt.org/docs/caa/>) :

- `validationmethods=dns-01` — « control which validation methods that CA can use » ;
- `accounturi=…` — « control which ACME Accounts can request issuance for the domain ».

**Sans délégation, ça ne vaut rien** : le jeton de zone réécrit le CAA lui-même.
**Avec délégation, ça devient une vraie serrure** : le CAA vit dans
`exemple.com`, hors d'atteinte des machines, et il nomme les comptes ACME
autorisés. Coût : chaque machine a son compte ACME Traefik, donc autant
d'`accounturi` à tenir à jour — une ligne CAA de plus à chaque machine ajoutée,
et un renouvellement qui casse si on l'oublie.

## 3. Une zone Cloudflare par machine ou par environnement

Un jeton par zone n'est utile que s'il y a plusieurs zones. Or le modèle tient
sur **un seul domaine**, l'environnement étant dans le chemin (`03-modele.md`).
Créer une zone par machine, c'est acheter un nom de domaine par machine, et
renoncer à `site.exemple.com` au profit de `site.exemple-machine2.com`. Le
produit change de forme pour un gain de sécurité que la délégation donne mieux
et moins cher. **À écarter.**

## 4. HTTP-01 sur les machines publiques

Aucun jeton sur la machine : c'est la seule piste qui supprime le secret au lieu
de le réduire.

| Fait | Source |
|---|---|
| « The HTTP-01 challenge can only be done on port 80 » | <https://letsencrypt.org/docs/challenge-types/> |
| « This challenge cannot be used to issue wildcard certificates » | même page |
| Les redirections sont suivies, « up to 10 redirects deep », vers les ports 80 ou 443 seulement | même page |
| Traefik doit être joignable sur le port du challenge (80 pour HTTP-01, 443 pour TLS-ALPN-01) | <https://doc.traefik.io/traefik/reference/install-configuration/tls/certificate-resolvers/acme/> |

Quand c'est possible : machine avec adresse publique, port 80 ouvert de bout en
bout, **aucun wildcard**, et le nom pointe déjà vers la machine — donc jamais
pour le premier certificat d'un nom qui n'est pas encore publié.

Quand c'est impossible : machine derrière NAT (le cas de premier rang du projet,
`06`), wildcard, et toute machine dont le port 80 est filtré par l'hébergeur.

**Le mélange vaut-il sa complexité ?** Deux résolveurs Traefik coexistent sans
problème, mais openCloud devrait savoir, **pour chaque nom**, lequel s'applique,
et le dire dans la page Certificats. Et le jour où une machine publique passe
derrière un NAT, ou demande un wildcard, il faut y reposer un jeton. C'est un
deuxième chemin d'exécution pour un gain partiel : **contraire à la règle « un
seul chemin »**. À garder comme repli documenté, pas comme voie principale.

## 5. Pistes écartées, en deux lignes chacune

| Piste | Pourquoi elle tombe |
|---|---|
| **Jetons courts émis par la machine openCloud** | Créer des jetons par l'API exige la permission « User > API Tokens > Edit » (<https://developers.cloudflare.com/fundamentals/api/how-to/create-via-api/>) : la machine openCloud détiendrait un secret qui fabrique n'importe quel jeton du compte — **strictement pire**. Et le renouvellement redeviendrait dépendant d'elle. |
| **Fournisseur `exec` / `httpreq` appelant openCloud** | Le TXT serait posé par la machine openCloud à la demande de Traefik. Aucun jeton sur les machines, mais **les renouvellements meurent quand elle s'éteint** : la règle qui a fait trancher le 8 septembre. |
| **Sous-zone Cloudflare `acme.exemple.net`** | Réservée à l'offre Enterprise (<https://developers.cloudflare.com/dns/zone-setups/subdomain-setup/>). Il faut un vrai second domaine, ce qui ramène à la piste 2a. |
| **Autorité intermédiaire / PKI privée** | Un certificat interne n'est pas reconnu par les navigateurs des visiteurs. Hors du besoin : ce qui est publié doit être public. |
| **Rôles Cloudflare limités à un domaine** | Les « Domain Scoped Roles » visent les membres d'un compte, pas les jetons d'API, et sont en accès anticipé (<https://blog.cloudflare.com/domain-scoped-roles-early-access/>). Ne descend pas non plus au niveau de l'enregistrement. |

## Recommandation

**Garder le jeton de zone pour la v0.1.0, et écrire la délégation comme une
option par zone, plus tard.** Le ticket #6 peut donc démarrer sans attendre.

Trois raisons, dans cet ordre :

1. **Rien de gratuit ne règle la crainte de Lucas.** Le filtrage par IP et le
   TTL protègent d'un fichier volé, pas d'une machine prise. Les proposer comme
   réponse serait se rassurer à bon compte.
2. **La seule vraie réponse coûte cher à l'opérateur, pas au code** : un CNAME
   par nom certifié, posé avant le premier certificat, pour toujours. Sur une
   infrastructure de trois machines et dix noms, c'est dix enregistrements de
   plus à ne pas casser.
3. **La délégation ne casse rien plus tard.** Elle se pose zone par zone,
   machine par machine, sans toucher au reste : la décision peut donc attendre
   d'avoir un premier certificat qui marche.

**La contrepartie, dite telle quelle** : jusqu'à ce que la délégation existe,
**une machine compromise reste une zone compromise**. C'est déjà écrit dans
`06-reseau-et-certificats.md` et dans `08-securite-et-secrets.md`. Cette étude ne
change pas le fait ; elle dit seulement qu'aucune des demi-mesures gratuites ne
le corrige, et que la mesure qui le corrige se paie en gestes d'opérateur.

Si Lucas veut la protection maintenant plutôt que plus tard, **c'est 2c (deSEC)
qui coûte le moins** : rien à héberger, un jeton par machine limité à un seul
nom et au seul type TXT, et le renouvellement continue quand la machine openCloud
est éteinte. Son défaut est ailleurs : un opérateur de plus dans la chaîne, et
gratuit.

## Les options à trancher

| Option | Ce qu'on gagne | Ce qu'on paie |
|---|---|---|
| **A. Ne rien changer** | Le ticket #6 démarre demain ; zéro composant en plus | Une machine compromise = une zone compromise. Le fait reste écrit dans `06` |
| **B. Statu quo + un jeton par machine, filtré par IP et daté** | Un jeton volé dans une sauvegarde ne sert plus ailleurs | Un jeton créé à la main par machine, refait à chaque changement d'adresse et à chaque expiration. **Ne protège pas d'une machine prise** |
| **C. Délégation vers acme-dns auto-hébergé, un compte par machine** | Le détournement du trafic **et** la certification au nom d'un autre deviennent impossibles ; rien ne sort de l'infrastructure | Une machine publique toujours allumée, un service DNS de plus à tenir et sauvegarder, un CNAME par nom, un aller-retour avant le premier certificat |
| **D. Délégation vers deSEC, un jeton par machine limité à un nom et au type TXT** | La même protection que C, sans serveur à tenir | Un opérateur DNS gratuit de plus dans la chaîne, une deuxième API à écrire dans openCloud, un CNAME par nom |

Dans tous les cas, **le mélange DNS-01 / HTTP-01 par machine (piste 4) reste à
écarter** comme voie principale : deuxième chemin d'exécution, pas de wildcard,
et rien pour les machines derrière NAT.

## Ce qui n'a pas pu être vérifié

| Point | Pourquoi |
|---|---|
| Le libellé exact et l'intitulé courant du groupe de permission DNS chez Cloudflare | Le tableau des permissions de zone n'a pas pu être lu en entier ; l'intitulé « Zone / DNS / Edit » vient de la documentation de lego, et Cloudflare renvoie au point d'appel *List permission groups* pour la liste à jour. Le fait qui compte — la zone est la ressource la plus fine — est confirmé deux fois |
| Le statut juridique et le financement de deSEC | Non lus sur une page officielle. Le service est annoncé comme gratuit ; **aucun engagement de disponibilité n'a été trouvé**, ce qui est en soi la réponse |
| Le comportement réel de lego quand la cible du CNAME est chez un autre fournisseur | Vérifié dans la documentation de Traefik et le code du fournisseur `acmedns`, jamais essayé sur une vraie zone. À éprouver avant de choisir C ou D |
| Le coût exact d'un second nom de domaine (piste 2a) | Dépend du registraire et de l'extension ; non chiffré ici |
