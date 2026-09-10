// Un faux Cloudflare pour le test de bout en bout : il joue
// GET /user/tokens/verify et GET /zones?name=, et rien d'autre. Les réponses
// ont la forme de l'API v4 (https://developers.cloudflare.com/api/).
//
// Il n'est ni installé, ni publié, ni packagé : le test le lance, l'interroge
// par la clé « cloudflare_api_url » de la configuration — développement
// seulement —, puis le tue. Un serveur HTTP jetable en bash n'aurait pas su
// refuser un jeton sur l'en-tête Authorization, et c'est justement ce que le
// test doit éprouver.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// Le serveur ne sert que quelques requêtes courtes, sur la boucle locale.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
)

type message struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Success  bool      `json:"success"`
	Errors   []message `json:"errors"`
	Messages []message `json:"messages"`
	Result   any       `json:"result"`
}

type zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9123", "adresse d'écoute")
	// Plusieurs jetons, séparés par une virgule : la rotation en éprouve deux.
	tokens := flag.String("tokens", "", "les jetons que ce faux Cloudflare accepte, séparés par une virgule")
	zoneName := flag.String("zone", "", "la seule zone que ces jetons voient")
	zoneID := flag.String("zone-id", "023e105f4ecef8ad9ca31a8372d0c353", "l'identifiant rendu pour cette zone")
	flag.Parse()

	if *tokens == "" || *zoneName == "" {
		fmt.Fprintln(os.Stderr, "usage : fake-cloudflare -tokens <jeton>[,<jeton>] -zone <zone> [-listen adresse]")
		os.Exit(2)
	}
	accepted := strings.Split(*tokens, ",")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /user/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, accepted) {
			refuse(w)
			return
		}
		reply(w, map[string]string{"id": "ed17574386854bf78a67040be0a770b0", "status": "active"})
	})
	mux.HandleFunc("GET /zones", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, accepted) {
			refuse(w)
			return
		}
		found := []zone{}
		if r.URL.Query().Get("name") == *zoneName {
			found = append(found, zone{ID: *zoneID, Name: *zoneName, Status: "active"})
		}
		reply(w, found)
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
	}
	fmt.Println("faux Cloudflare sur " + *listen)
	log.Fatal(server.ListenAndServe())
}

func authorized(r *http.Request, accepted []string) bool {
	return slices.Contains(accepted, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func reply(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope{Success: true, Errors: []message{}, Messages: []message{}, Result: result})
}

func refuse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(envelope{
		Errors:   []message{{Code: 1000, Message: "Invalid API Token"}},
		Messages: []message{},
	})
}
