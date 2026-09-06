// Package runner tient une file par machine et le journal de transaction des
// actions : il prépare, dépose, lance, suit journald, conclut, et reprend au
// démarrage tout ce qui était préparé ou en cours. Il ne compose aucune
// commande et ne sait rien de SSH : le catalogue et le transport lui sont
// donnés.
package runner
