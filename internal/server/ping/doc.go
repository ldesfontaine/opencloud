// Package ping sert les routes /ping/… qu'un cron appelle à la fin ou au
// début d'une tâche, sans authentification : le jeton de l'URL est le
// secret, et le débit est borné par source et par jeton.
package ping
