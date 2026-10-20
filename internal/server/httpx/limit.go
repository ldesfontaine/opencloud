package httpx

import "net/http"

// RetryAfterSeconds est l'attente dite à un refus de débit, la même pour
// l'administration, l'agent, les pings et la page publique.
const RetryAfterSeconds = "2"

// WriteTooMany répond 429 en texte : un mot, jamais une clé de catalogue.
// L'agent et les pings gardent leur format et ne prennent que l'attente.
func WriteTooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", RetryAfterSeconds)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}
