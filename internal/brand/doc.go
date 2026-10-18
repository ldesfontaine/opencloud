// Package brand est le seul endroit où le nom du produit s'écrit. Il ne
// dépend de rien et ne porte que des constantes, en trois étages qui ne
// dérivent pas l'un de l'autre :
//
//   - le nom affiché, ce que lit l'opérateur ;
//   - l'identifiant technique, d'où viennent les chemins et les fichiers ;
//   - l'empreinte du protocole, des littéraux figés : en-têtes, préfixes
//     de jetons, identifiants échangés avec les agents et les récepteurs.
//
// Un test (TestNameOnlyInBrand) échoue si le nom ou l'un des préfixes de
// jeton réapparaît en littéral ailleurs dans le code Go : pour l'écrire, on
// passe par ce paquet.
package brand
