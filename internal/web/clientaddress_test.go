package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientAddress_TrustsXForwardedForOnlyBehindADeclaredProxy(t *testing.T) {
	cases := map[string]struct {
		remoteAddress  string
		forwardedFor   []string
		trustedProxies []string
		want           string
	}{
		"sans proxy déclaré, l'adresse est celle de la connexion": {
			remoteAddress: "203.0.113.7:54321",
			want:          "203.0.113.7",
		},
		"sans proxy déclaré, un en-tête forgé est ignoré": {
			remoteAddress: "203.0.113.7:54321",
			forwardedFor:  []string{"10.9.9.9"},
			want:          "203.0.113.7",
		},
		"derrière un proxy de confiance, l'en-tête fait foi": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"203.0.113.7"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "203.0.113.7",
		},
		"un proxy déclaré par son adresse seule": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"203.0.113.7"},
			trustedProxies: []string{"10.0.0.2"},
			want:           "203.0.113.7",
		},
		"plusieurs sauts : le dernier hors des proxies de confiance": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"198.51.100.9, 203.0.113.7, 10.0.0.3"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "203.0.113.7",
		},
		"plusieurs en-têtes valent une seule chaîne": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"198.51.100.9", "203.0.113.7, 10.0.0.3"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "203.0.113.7",
		},
		"une chaîne forgée derrière le proxy ne remonte pas plus loin que le client": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"1.1.1.1, 203.0.113.7"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "203.0.113.7",
		},
		"un saut illisible rend l'adresse de la connexion, qui ne se forge pas": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"pas-une-adresse"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "10.0.0.2",
		},
		"une chaîne entièrement de confiance rend l'adresse de la connexion": {
			remoteAddress:  "10.0.0.2:54321",
			forwardedFor:   []string{"10.0.0.3, 10.0.0.4"},
			trustedProxies: []string{"10.0.0.0/8"},
			want:           "10.0.0.2",
		},
		"une adresse IPv4 mappée en IPv6 est la même adresse": {
			remoteAddress: "[::ffff:203.0.113.7]:54321",
			want:          "203.0.113.7",
		},
		"un proxy IPv6 de confiance": {
			remoteAddress:  "[2001:db8::2]:54321",
			forwardedFor:   []string{"203.0.113.7"},
			trustedProxies: []string{"2001:db8::/32"},
			want:           "203.0.113.7",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/login", nil)
			request.RemoteAddr = testCase.remoteAddress
			for _, value := range testCase.forwardedFor {
				request.Header.Add(forwardedForHeader, value)
			}

			got := clientAddress(request, parseProxies(t, testCase.trustedProxies))

			if got.String() != testCase.want {
				t.Fatalf("adresse = %s, attendu %s", got, testCase.want)
			}
		})
	}
}

func TestClientAddress_UnreadableConnection_IsNoAddress(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "@"

	if address := clientAddress(request, nil); address.IsValid() {
		t.Fatalf("attendu aucune adresse, reçu %s", address)
	}
}

func parseProxies(t *testing.T, entries []string) []netip.Prefix {
	t.Helper()
	var prefixes []netip.Prefix
	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			address := netip.MustParseAddr(entry)
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}
