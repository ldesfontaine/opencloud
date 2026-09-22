package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRegistry joue un registre v2 : un défi Bearer sur tout /v2, un
// realm qui rend un jeton, des tags paginés par Link, des manifestes en
// HEAD. Le dépôt « private/app » exige des identifiants au realm.
type fakeRegistry struct {
	server       *httptest.Server
	tokenCalls   atomic.Int32
	tags         map[string][]string
	digests      map[string]string
	basicOnly    bool
	noDigestHead bool
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fake := &fakeRegistry{
		tags: map[string][]string{
			"library/alpine": {"2.6", "3.19", "3.20", "3.21", "3.22", "latest"},
			"private/app":    {"1.0.0", "1.1.0"},
		},
		digests: map[string]string{
			"library/alpine:3.20": "sha256:" + strings.Repeat("a", 64),
			"library/alpine:3.22": "sha256:" + strings.Repeat("b", 64),
			"private/app:1.1.0":   "sha256:" + strings.Repeat("c", 64),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", fake.token)
	mux.HandleFunc("/v2/", fake.v2)
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeRegistry) host() string {
	return strings.TrimPrefix(f.server.URL, "http://")
}

func (f *fakeRegistry) token(w http.ResponseWriter, r *http.Request) {
	f.tokenCalls.Add(1)
	if strings.Contains(r.URL.Query().Get("scope"), "private/") {
		user, password, ok := r.BasicAuth()
		if !ok || user != "lucas" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"token": "good", "expires_in": 300})
}

func (f *fakeRegistry) v2(w http.ResponseWriter, r *http.Request) {
	if f.basicOnly {
		if user, password, ok := r.BasicAuth(); !ok || user != "lucas" || password != "secret" {
			w.Header().Set("Www-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	} else if r.Header.Get("Authorization") != "Bearer good" {
		w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`, f.server.URL, repositoryOf(r.URL.Path)))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/tags/list"):
		f.serveTags(w, r)
	case strings.Contains(r.URL.Path, "/manifests/"):
		f.serveManifest(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func repositoryOf(path string) string {
	path = strings.TrimPrefix(path, "/v2/")
	if index := strings.Index(path, "/tags/"); index >= 0 {
		return path[:index]
	}
	if index := strings.Index(path, "/manifests/"); index >= 0 {
		return path[:index]
	}
	return path
}

// Deux tags par page, pour exercer la pagination sans mille tags.
func (f *fakeRegistry) serveTags(w http.ResponseWriter, r *http.Request) {
	tags, ok := f.tags[repositoryOf(r.URL.Path)]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	start := 0
	if last := r.URL.Query().Get("last"); last != "" {
		for index, tag := range tags {
			if tag == last {
				start = index + 1
			}
		}
	}
	end := min(start+2, len(tags))
	if end < len(tags) {
		w.Header().Set("Link", fmt.Sprintf(`</v2/%s/tags/list?last=%s&n=2>; rel="next"`, repositoryOf(r.URL.Path), tags[end-1]))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"name": repositoryOf(r.URL.Path), "tags": tags[start:end]})
}

func (f *fakeRegistry) serveManifest(w http.ResponseWriter, r *http.Request) {
	repository, tag, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/manifests/")
	digest, ok := f.digests[repository+":"+tag]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}
	if !f.noDigestHead {
		w.Header().Set("Docker-Content-Digest", digest)
	}
	w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`{"schemaVersion":2}`))
	}
}

func reference(t *testing.T, fake *fakeRegistry, image string) Reference {
	t.Helper()
	ref, err := Parse(fake.host() + "/" + image)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestTags_FollowsPagination_WithOneToken(t *testing.T) {
	fake := newFakeRegistry(t)
	client := New(nil)
	ref := reference(t, fake, "library/alpine:3.20")
	tags, err := client.Tags(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tags, ",") != "2.6,3.19,3.20,3.21,3.22,latest" {
		t.Fatalf("tags = %v", tags)
	}
	digest, err := client.Digest(context.Background(), ref)
	if err != nil || digest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("digest = %q, %v", digest, err)
	}
	if calls := fake.tokenCalls.Load(); calls != 1 {
		t.Fatalf("token fetched %d times, want once for the repository", calls)
	}
}

func TestDigest_FallsBackToGet_WhenHeadHasNoDigest(t *testing.T) {
	fake := newFakeRegistry(t)
	fake.noDigestHead = true
	digest, err := New(nil).Digest(context.Background(), reference(t, fake, "library/alpine:3.22"))
	if err != nil || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		t.Fatalf("digest = %q, %v", digest, err)
	}
}

func TestPrivateRepository_NeedsKeychain(t *testing.T) {
	fake := newFakeRegistry(t)
	ref := reference(t, fake, "private/app:1.0.0")
	_, err := New(nil).Tags(context.Background(), ref)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("anonymous: err = %v, want ErrUnauthorized", err)
	}
	keychain := Static{fake.host(): {Username: "lucas", Password: "secret"}}
	tags, err := New(keychain).Tags(context.Background(), ref)
	if err != nil || len(tags) != 2 {
		t.Fatalf("signed: tags = %v, %v", tags, err)
	}
}

func TestBasicRegistry_UsesKeychain(t *testing.T) {
	fake := newFakeRegistry(t)
	fake.basicOnly = true
	ref := reference(t, fake, "private/app:1.1.0")
	if _, err := New(nil).Digest(context.Background(), ref); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("anonymous: err = %v", err)
	}
	digest, err := New(Static{fake.host(): {Username: "lucas", Password: "secret"}}).Digest(context.Background(), ref)
	if err != nil || digest != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("digest = %q, %v", digest, err)
	}
}

func TestErrors_AreSentinels(t *testing.T) {
	fake := newFakeRegistry(t)
	client := New(nil)
	if _, err := client.Tags(context.Background(), reference(t, fake, "library/nope:1")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown repository: %v", err)
	}
	if _, err := client.Digest(context.Background(), reference(t, fake, "library/alpine:9.9")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tag: %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	ref, _ := Parse(strings.TrimPrefix(down.URL, "http://") + "/x/y:1")
	if _, err := client.Tags(context.Background(), ref); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("502: %v", err)
	}
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }))
	defer redirecting.Close()
	ref, _ = Parse(strings.TrimPrefix(redirecting.URL, "http://") + "/x/y:1")
	if _, err := client.Tags(context.Background(), ref); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("redirect must not be followed: %v", err)
	}
}

func TestParse_ReadsWhatDockerWrites(t *testing.T) {
	cases := []struct {
		image    string
		want     Reference
		familiar string
	}{
		{"alpine", Reference{DockerHub, "library/alpine", "latest", ""}, "alpine"},
		{"alpine:3.20", Reference{DockerHub, "library/alpine", "3.20", ""}, "alpine"},
		{"docker.io/library/nginx:1.27", Reference{DockerHub, "library/nginx", "1.27", ""}, "nginx"},
		{"prom/node-exporter:v1.8.2", Reference{DockerHub, "prom/node-exporter", "v1.8.2", ""}, "prom/node-exporter"},
		{"ghcr.io/ldesfontaine/portfolio:0.0.2", Reference{"ghcr.io", "ldesfontaine/portfolio", "0.0.2", ""}, "ghcr.io/ldesfontaine/portfolio"},
		{"localhost:5000/app", Reference{"localhost:5000", "app", "latest", ""}, "localhost:5000/app"},
		{"nginx@sha256:" + strings.Repeat("0", 64), Reference{DockerHub, "library/nginx", "", "sha256:" + strings.Repeat("0", 64)}, "nginx"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.image)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %+v, %v ; want %+v", tc.image, got, err, tc.want)
		}
		if got.Familiar() != tc.familiar {
			t.Errorf("Parse(%q).Familiar() = %q, want %q", tc.image, got.Familiar(), tc.familiar)
		}
	}
	for _, bad := range []string{"", "Alpine", "alpine:", "alpine:tag with space", "a@sha256:short", "/x", "x//y"} {
		if _, err := Parse(bad); !errors.Is(err, ErrBadReference) {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	if ref, _ := Parse("alpine:3.20"); ref.String() != "alpine:3.20" || ref.WithTag("3.22").String() != "alpine:3.22" {
		t.Fatalf("String/WithTag: %q", ref.String())
	}
}

func TestLoadDockerConfig_ReadsInlineAuths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"auths": {
		"https://index.docker.io/v1/": {"auth": "bHVjYXM6aHVi"},
		"ghcr.io": {"username": "lucas", "password": "ghp"},
		"broken.example": {"auth": "%%%"},
		"helper.example": {}
	}, "credHelpers": {"gcr.io": "gcloud"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	keychain, err := LoadDockerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := keychain.Lookup(DockerHub); !ok || got != (Credentials{"lucas", "hub"}) {
		t.Fatalf("hub = %+v %v", got, ok)
	}
	if got, ok := keychain.Lookup("ghcr.io"); !ok || got != (Credentials{"lucas", "ghp"}) {
		t.Fatalf("ghcr = %+v %v", got, ok)
	}
	if len(keychain) != 2 {
		t.Fatalf("keychain = %+v", keychain)
	}
	missing, err := LoadDockerConfig(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing file: %+v %v", missing, err)
	}
}
