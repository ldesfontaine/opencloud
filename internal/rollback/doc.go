// Package rollback tient le retour arrière d'une mise à jour d'agent : le
// marqueur « mise à jour en attente de confirmation » que l'agent écrit avant
// de basculer son lien, les liens current et previous, et le lanceur stable
// (Guard) qui rebascule seul quand rien n'a confirmé à l'échéance.
//
// Il ne parle pas au serveur et ne connaît pas les ordres : l'agent écrit et
// confirme le marqueur, le lanceur le juge. Aucun shell, aucune entrée du
// réseau : le lanceur ne lit que le marqueur, que le compte de l'agent écrit.
package rollback
