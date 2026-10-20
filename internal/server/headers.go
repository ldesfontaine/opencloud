package server

import "net/http"

// CSP stricte : aucun style ni script en ligne, rien depuis Internet.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// La même politique, sauf frame-ancestors : la page de statut publique
// se met dans le cadre d'un portail ou d'une documentation. Elle seule :
// elle ne porte aucun bouton, il n'y a rien à y détourner par un clic ;
// l'administration et l'API, elles, restent interdites de cadre.
const embeddablePolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors *; " +
	"base-uri 'none'"

// La page de statut publique vit sous /statut (paquet public). Le chemin
// est connu ici parce que la politique de cadre se décide par lui.
const statusPath = "/statut"

// isPublicStatusPage dit si le chemin est la page HTML publique, la seule
// réponse qui relâche frame-ancestors : c'est elle qu'un portail ou une
// documentation met dans un cadre, jamais son API ni l'administration.
func isPublicStatusPage(path string) bool {
	return path == statusPath || path == statusPath+"/"
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		if isPublicStatusPage(r.URL.Path) {
			headers.Set("Content-Security-Policy", embeddablePolicy)
		} else {
			headers.Set("Content-Security-Policy", contentSecurityPolicy)
			headers.Set("X-Frame-Options", "DENY")
		}
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("Referrer-Policy", "same-origin")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
