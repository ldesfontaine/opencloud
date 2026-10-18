// Package seal chiffre les secrets que l'opérateur saisit dans l'interface
// (mot de passe du relais, URL et secret des canaux, jeton de ping) avant
// qu'ils n'entrent en base. La clé vit dans un fichier à côté de la base,
// jamais dedans : une copie de la base seule ne livre plus ces secrets.
//
// Chaque chiffré est lié à sa table, sa colonne et sa ligne : recopié
// ailleurs, il ne s'ouvre plus. Le paquet ne connaît ni SQL ni HTTP et
// n'emploie que la bibliothèque standard.
package seal
