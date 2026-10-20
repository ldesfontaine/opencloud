// Package directory est l'annuaire : les projets, les services déclarés et
// leurs unités. Il garde ce que l'opérateur déclare, jamais ce que Docker
// montre ; il ne parle ni HTTP ni SQL, le serveur l'expose et le store
// l'écrit. Le paquet container, lui, reste celui des conteneurs observés.
package directory
