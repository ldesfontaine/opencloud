// Package systemd prévient systemd de l'état du service par sd_notify, pour
// une unité Type=notify. Sans NOTIFY_SOCKET, chaque appel est un non-événement :
// le binaire se lance aussi à la main.
package systemd
