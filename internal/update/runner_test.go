package update

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/registry"
)

type fakeDocker struct {
	containers []dockerapi.Summary
	images     map[string]dockerapi.Image
	err        error
}

func (f *fakeDocker) ListContainers(context.Context) ([]dockerapi.Summary, error) {
	return f.containers, f.err
}

func (f *fakeDocker) InspectImage(_ context.Context, reference string) (dockerapi.Image, error) {
	image, ok := f.images[reference]
	if !ok {
		return dockerapi.Image{}, dockerapi.ErrNotFound
	}
	return image, nil
}

type fakeRegistry struct {
	tags    map[string][]string
	digests map[string]string
	errs    map[string]error
}

func (f *fakeRegistry) Tags(_ context.Context, ref registry.Reference) ([]string, error) {
	if err := f.errs[ref.Repository]; err != nil {
		return nil, err
	}
	return f.tags[ref.Repository], nil
}

func (f *fakeRegistry) Digest(_ context.Context, ref registry.Reference) (string, error) {
	if err := f.errs[ref.Repository]; err != nil {
		return "", err
	}
	return f.digests[ref.Repository+":"+ref.Tag], nil
}

type collector struct {
	mu      sync.Mutex
	reports []Report
	got     chan struct{}
}

func (c *collector) Deliver(report Report) {
	c.mu.Lock()
	c.reports = append(c.reports, report)
	c.mu.Unlock()
	select {
	case c.got <- struct{}{}:
	default:
	}
}

func (c *collector) results() []Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []Result
	for _, report := range c.reports {
		all = append(all, report.Results...)
	}
	return all
}

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
	digestC = "sha256:" + strings.Repeat("c", 64)
	now     = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
)

func newChecker() (*Checker, *fakeDocker, *fakeRegistry) {
	docker := &fakeDocker{images: map[string]dockerapi.Image{
		"alpine:3.20":                       {RepoDigests: []string{"alpine@" + digestA}},
		"postgres:16":                       {RepoDigests: []string{"postgres@" + digestA}},
		"portfolio:latest":                  {},
		"ghcr.io/ldesfontaine/app:1.0.0":    {RepoDigests: []string{"ghcr.io/ldesfontaine/app@" + digestA}},
		"nginx@" + digestA:                  {RepoDigests: []string{"nginx@" + digestA}},
		"keinos/sqlite3:latest":             {RepoDigests: []string{"keinos/sqlite3@" + digestA}},
		"registry.example.internal/x:1.2.3": {RepoDigests: []string{"registry.example.internal/x@" + digestA}},
	}}
	remote := &fakeRegistry{
		tags: map[string][]string{
			"library/alpine":   {"3.19", "3.20", "3.21", "3.22", "latest"},
			"library/postgres": {"15", "16", "17", "16.4"},
			"keinos/sqlite3":   {"latest", "3.46"},
		},
		digests: map[string]string{
			"library/alpine:3.20":   digestA,
			"library/alpine:3.22":   digestB,
			"library/postgres:16":   digestC,
			"keinos/sqlite3:latest": digestA,
		},
		errs: map[string]error{
			"ldesfontaine/app": registry.ErrUnauthorized,
			"x":                errors.New("dial tcp: no route"),
		},
	}
	return NewChecker(docker, remote), docker, remote
}

func TestChecker_ReportsFactsPerOutcome(t *testing.T) {
	checker, _, _ := newChecker()
	cases := []struct {
		image string
		want  Result
	}{
		{"alpine:3.20", Result{Outcome: OutcomeOK, LocalDigest: digestA, RemoteDigest: digestA, NewerTag: "3.22", NewerDigest: digestB}},
		{"postgres:16", Result{Outcome: OutcomeOK, LocalDigest: digestA, RemoteDigest: digestC, NewerTag: "17"}},
		{"keinos/sqlite3:latest", Result{Outcome: OutcomeOK, LocalDigest: digestA, RemoteDigest: digestA}},
		{"portfolio:latest", Result{Outcome: OutcomeLocal}},
		{"ghcr.io/ldesfontaine/app:1.0.0", Result{Outcome: OutcomeUnauthorized, LocalDigest: digestA}},
		{"registry.example.internal/x:1.2.3", Result{Outcome: OutcomeUnreachable, LocalDigest: digestA}},
		{"nginx@" + digestA, Result{Outcome: OutcomeUnsupported}},
		{"Not An Image", Result{Outcome: OutcomeUnsupported}},
	}
	for _, tc := range cases {
		got, err := checker.Check(context.Background(), tc.image, now)
		if err != nil {
			t.Fatalf("%s: %v", tc.image, err)
		}
		tc.want.Image, tc.want.CheckedAt = tc.image, now
		if got != tc.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.image, got, tc.want)
		}
	}
	if _, err := checker.Check(context.Background(), "gone:1", now); !errors.Is(err, dockerapi.ErrNotFound) {
		t.Fatalf("an image Docker no longer has must be an error, got %v", err)
	}
}

func newRunner(t *testing.T, docker *fakeDocker, checker ImageChecker) (*Runner, *collector) {
	t.Helper()
	sink := &collector{got: make(chan struct{}, 1)}
	runner := NewRunner(docker, checker, sink, slog.New(slog.DiscardHandler))
	runner.timing = timing{first: 10 * time.Millisecond, discovery: 20 * time.Millisecond, interval: time.Hour, pause: 0}
	return runner, sink
}

func waitReport(t *testing.T, sink *collector) {
	t.Helper()
	select {
	case <-sink.got:
	case <-time.After(2 * time.Second):
		t.Fatal("no report delivered")
	}
}

func TestRunner_ChecksDistinctImagesOnce_ThenNewOnes(t *testing.T) {
	checker, docker, _ := newChecker()
	docker.containers = []dockerapi.Summary{
		{Image: "alpine:3.20"}, {Image: "alpine:3.20"}, {Image: "portfolio:latest"},
		{Image: "postgres:16", Labels: map[string]string{dockerapi.LabelComposeOneOff: "True"}},
	}
	runner, sink := newRunner(t, docker, checker)
	runner.Assign(Assignment{Excluded: []string{"portfolio:latest"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitReport(t, sink)
	results := sink.results()
	if len(results) != 1 || results[0].Image != "alpine:3.20" {
		t.Fatalf("first pass = %+v", results)
	}
	// Un conteneur de plus : son image est vérifiée au tour suivant, les
	// autres attendent leur jour.
	docker.containers = append(docker.containers, dockerapi.Summary{Image: "keinos/sqlite3:latest"})
	waitReport(t, sink)
	results = sink.results()
	if len(results) != 2 || results[1].Image != "keinos/sqlite3:latest" {
		t.Fatalf("second pass = %+v", results)
	}
	// « Maintenant » revérifie tout.
	runner.Assign(Assignment{Excluded: []string{"portfolio:latest"}, Now: true})
	waitReport(t, sink)
	if results = sink.results(); len(results) != 4 {
		t.Fatalf("forced pass = %+v", results)
	}
}

type failingChecker struct{}

func (failingChecker) Check(context.Context, string, time.Time) (Result, error) {
	return Result{}, dockerapi.ErrEngineDown
}

func TestRunner_SaysNothingWhenDockerIsSilent(t *testing.T) {
	docker := &fakeDocker{containers: []dockerapi.Summary{{Image: "alpine:3.20"}}}
	runner, sink := newRunner(t, docker, failingChecker{})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	runner.Run(ctx)
	if len(sink.results()) != 0 {
		t.Fatalf("delivered %+v", sink.results())
	}
}
