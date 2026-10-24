package agent

import (
	"context"
	"errors"
	"io"
	"sync"
)

// workerQueue : combien d'ordres longs attendent leur tour. Un serveur sain
// en envoie deux par nuit ; au-delà, l'agent dit qu'il est occupé.
const workerQueue = 16

// ErrWorkerBusy : la file des ordres longs est pleine.
var ErrWorkerBusy = errors.New("long order queue full")

// orderWorker exécute les ordres longs (recevoir une sauvegarde, plus tard
// déployer ou se mettre à jour) un à la fois, dans l'ordre où ils sont
// arrivés : le rang croît déjà dans cet ordre. Le juge, lui, ne les attend
// pas : il répond « accepté » et passe au suivant. La goroutine vit avec
// l'agent, pas avec le flux.
type orderWorker struct {
	queue chan func(ctx context.Context)
}

func newOrderWorker() *orderWorker {
	return &orderWorker{queue: make(chan func(context.Context), workerQueue)}
}

// run exécute les ordres jusqu'à l'arrêt de l'agent.
func (w *orderWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.queue:
			job(ctx)
		}
	}
}

// submit met un ordre en file ; faux si elle est pleine.
func (w *orderWorker) submit(job func(ctx context.Context)) bool {
	select {
	case w.queue <- job:
		return true
	default:
		return false
	}
}

// fetcher télécharge les colis du serveur sous le jeton de session du flux
// courant. Ce jeton change à chaque reconnexion : il est lu au moment du
// téléchargement, et vide hors connexion. Un téléchargement commencé sur une
// session tombée échoue ; il n'est pas repris.
type fetcher struct {
	mu      sync.Mutex
	client  *Client
	session string
}

var errNoSession = errors.New("no open stream to download from")

func (f *fetcher) use(client *Client) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.client = client
}

func (f *fetcher) setSession(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session = token
}

func (f *fetcher) open(ctx context.Context, parcel, name string) (io.ReadCloser, int64, error) {
	f.mu.Lock()
	client, session := f.client, f.session
	f.mu.Unlock()
	if client == nil || session == "" {
		return nil, 0, errNoSession
	}
	return client.GetParcel(ctx, session, parcel, name)
}

// put téléverse un fichier dans un colis du serveur, sous le jeton de session
// du flux courant. Comme un téléchargement, un envoi commencé sur une session
// tombée échoue ; l'exécuteur de l'ordre le relance.
func (f *fetcher) put(ctx context.Context, parcel string, file parcelUpload) error {
	f.mu.Lock()
	client, session := f.client, f.session
	f.mu.Unlock()
	if client == nil || session == "" {
		return errNoSession
	}
	body, err := file.open()
	if err != nil {
		return err
	}
	defer body.Close()
	return client.PutParcel(ctx, session, parcel, file.name, body, file.size, file.sum)
}

// parcelUpload est un fichier prêt à partir : sa taille et sa somme sont celles
// du disque, open le relit à chaque tentative.
type parcelUpload struct {
	name string
	size int64
	sum  string
	open func() (io.ReadCloser, error)
}

// parcelSink est ce que l'exécuteur de backup.produce attend du téléversement.
type parcelSink interface {
	put(ctx context.Context, parcel string, file parcelUpload) error
}

// parcelSource est ce que l'exécuteur d'un ordre attend du téléchargement.
type parcelSource interface {
	open(ctx context.Context, parcel, name string) (io.ReadCloser, int64, error)
}
