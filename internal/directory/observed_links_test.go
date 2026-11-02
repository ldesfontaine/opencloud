package directory

import "testing"

func TestDeclaredHostPort(t *testing.T) {
	cases := []struct {
		declared string
		origin   Origin
		want     int
	}{
		{"8080:80", OriginContainer, 8080},
		{"8080:80/tcp", OriginCompose, 8080},
		{"80", OriginContainer, 0}, // le port du conteneur, pas publié
		{"443", OriginBinary, 443}, // le port qu'un binaire écoute sur l'hôte
		{"80/tcp", OriginBinary, 80},
		{"53/udp", OriginBinary, 0}, // la table lue est TCP
		{"5353:53/udp", OriginContainer, 0},
		{"0", OriginBinary, 0},
		{"70000", OriginBinary, 0},
		{"x", OriginBinary, 0},
	}
	for _, tc := range cases {
		got, found := declaredHostPort(tc.declared, tc.origin)
		if got != tc.want || found != (tc.want != 0) {
			t.Errorf("declaredHostPort(%q, %s) = %d, %v; want %d", tc.declared, tc.origin, got, found, tc.want)
		}
	}
}
