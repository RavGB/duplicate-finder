package main

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
)

func TestBuildGroupsSortingAndWasted(t *testing.T) {
	hashA := [sha256.Size]byte{1}
	hashB := [sha256.Size]byte{2}
	hashC := [sha256.Size]byte{3}
	hashD := [sha256.Size]byte{4}
	grouped := map[int64]map[[sha256.Size]byte][]string{
		100: {
			hashA: {"/z/b", "/z/a"}, // wasted 100, first path /z/a
			hashD: {"/m/b", "/m/a"}, // wasted 100, first path /m/a (wins the tie)
		},
		50: {
			hashC: {"/a/z", "/a/x", "/a/y"}, // wasted 100, smaller size sorts later
		},
		10: {
			hashB: {"/q/1", "/q/2", "/q/3", "/q/4", "/q/5"}, // wasted 40, sorts last
		},
		7: {
			hashA: {"/only/one"}, // a single surviving file is not a group
		},
	}

	groups := buildGroups(grouped)
	if len(groups) != 4 {
		t.Fatalf("groups = %d, want 4 (single file excluded)", len(groups))
	}
	wantOrder := []struct {
		size   int64
		wasted int64
		first  string
	}{
		{100, 100, "/m/a"},
		{100, 100, "/z/a"},
		{50, 100, "/a/x"},
		{10, 40, "/q/1"},
	}
	for i, want := range wantOrder {
		g := groups[i]
		if g.Size != want.size || g.WastedBytes != want.wasted || g.Paths[0] != want.first {
			t.Errorf("group %d = size %d wasted %d first %q, want size %d wasted %d first %q",
				i, g.Size, g.WastedBytes, g.Paths[0], want.size, want.wasted, want.first)
		}
	}
	// Paths inside a group are sorted even though the map gave them
	// out of order.
	if groups[2].Paths[1] != "/a/y" || groups[2].Paths[2] != "/a/z" {
		t.Errorf("group paths not sorted: %v", groups[2].Paths)
	}
	// Copies and wasted math: 3 copies of 50 bytes waste 2 x 50.
	if groups[2].Copies != 3 || groups[2].WastedBytes != 100 {
		t.Errorf("group = %+v, want 3 copies wasting 100", groups[2])
	}
	if groups[0].Hash == "" || len(groups[0].Hash) != 64 {
		t.Errorf("hash = %q, want a 64 character hex SHA-256", groups[0].Hash)
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	stats := &scanStats{FilesScanned: 9, CandidatesHashed: 4, SkippedEmpty: 2}
	grouped := map[int64]map[[sha256.Size]byte][]string{
		11: {{9}: {"/tmp/x/a", "/tmp/x/b"}},
	}
	report := buildReport("/tmp/x", grouped, stats)

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Root != "/tmp/x" || back.GroupCount != 1 || back.TotalWastedBytes != 11 {
		t.Errorf("round trip report = %+v", back)
	}
	if back.FilesScanned != 9 || back.CandidatesHashed != 4 || back.SkippedEmpty != 2 {
		t.Errorf("round trip counters = %+v", back)
	}
	if len(back.Groups) != 1 || back.Groups[0].Copies != 2 || back.Groups[0].Paths[0] != "/tmp/x/a" {
		t.Errorf("round trip groups = %+v", back.Groups)
	}
}

func TestEmptyReportJSONHasGroupsArray(t *testing.T) {
	report := buildReport("/tmp/x", map[int64]map[[sha256.Size]byte][]string{}, &scanStats{})
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	groups, ok := raw["groups"].([]any)
	if !ok || len(groups) != 0 {
		t.Errorf("groups field = %v, want an empty JSON array, not null", raw["groups"])
	}
}
