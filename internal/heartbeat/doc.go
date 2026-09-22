// Package heartbeat surveille des tâches planifiées par le ping qu'elles
// envoient : un moniteur attend un ping avant son échéance, sinon il passe
// en retard. Il ne parle ni HTTP ni SQL : le web reçoit les pings, le store
// les écrit. Les alertes passent par Listener, le direct par Watcher.
package heartbeat
