// Package deletion porte le plan d'une suppression : ce qui la retient
// (restrict), ce qu'elle emporte (cascade, compté) et ce qu'elle détache
// (set null, lien seul). Il ne lit ni ne supprime rien : la base compte, le
// domaine refuse tant que le plan est bloqué.
package deletion
