// Package settings lit et écrit les réglages de l'opérateur, ceux qu'il
// change dans l'interface. Ils vivent dans la base, sur une seule ligne
// (internal/store) ; ce qui les change passe par Service, qui porte les
// règles de chaque clé.
package settings
