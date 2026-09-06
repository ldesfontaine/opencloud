// Package store tient l'état d'openCloud dans SQLite : comptes, sessions, et
// plus tard machines, services, domaines, actions. Les requêtes vivent ici et
// nulle part ailleurs. Open crée la base si elle manque, refuse de l'ouvrir
// quand elle porte un schéma que le binaire ne connaît pas, la sauvegarde
// avant toute migration, puis applique les migrations en attente.
package store
