package agentwire

import (
	"strings"
	"testing"
)

func TestResticBrowseParams_AcceptOnlyAFormTheAgentCanRunSafely(t *testing.T) {
	id := strings.Repeat("a", 64)
	good := []ResticBrowseParams{{User: "11111111-2222-4333-8444-555555555555"}, {User: "11111111-2222-4333-8444-555555555555", Snapshot: id, Path: "/"}, {User: "11111111-2222-4333-8444-555555555555", Snapshot: id, Path: "/srv/a b"}}
	for _, params := range good {
		if err := params.Validate(); err != nil {
			t.Errorf("%+v: %v", params, err)
		}
	}
	bad := []ResticBrowseParams{
		{User: "x/../y"}, {User: good[0].User, Path: "/srv"}, {User: good[0].User, Snapshot: "latest", Path: "/"},
		{User: good[0].User, Snapshot: id, Path: ""}, {User: good[0].User, Snapshot: id, Path: "srv"}, {User: good[0].User, Snapshot: id, Path: "/a/../b"},
		{User: good[0].User, Snapshot: id, Path: "/a//b"}, {User: good[0].User, Snapshot: id, Path: "/a/"}, {User: good[0].User, Snapshot: id, Path: "/a\x00"},
	}
	for _, params := range bad {
		if params.Validate() == nil {
			t.Errorf("%+v was accepted", params)
		}
	}
}

func TestResticBrowseReport_IsBounded(t *testing.T) {
	if err := (ResticBrowseReport{Entries: []BrowseEntry{{Name: "a.txt", Kind: "file", Size: 1}}}).Validate(); err != nil {
		t.Fatal(err)
	}
	tooMany := ResticBrowseReport{Entries: make([]BrowseEntry, MaxBrowseEntries+1)}
	for _, report := range []ResticBrowseReport{
		tooMany,
		{Entries: []BrowseEntry{{Name: "a/b", Kind: "file"}}},
		{Entries: []BrowseEntry{{Name: "..", Kind: "dir"}}},
		{Entries: []BrowseEntry{{Name: "a", Kind: "socket"}}},
		{Entries: []BrowseEntry{{Name: "a", Kind: "file", Size: -1}}},
		{Snapshots: []BrowseSnapshot{{ID: "short", Time: "x"}}},
	} {
		if report.Validate() == nil {
			t.Errorf("%+v was accepted", report)
		}
	}
}

func TestResticBrowseSecret_HidesItselfInALog(t *testing.T) {
	if got := (ResticBrowseSecret{RepoPassword: "x"}).String(); got != redactedResticSecret {
		t.Fatal(got)
	}
}
