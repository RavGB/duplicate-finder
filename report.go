package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Group is one set of files with identical content.
type Group struct {
	Size        int64    `json:"size_bytes"`
	SizeHuman   string   `json:"size"`
	Hash        string   `json:"sha256"`
	Copies      int      `json:"copies"`
	WastedBytes int64    `json:"wasted_bytes"`
	WastedHuman string   `json:"wasted"`
	Paths       []string `json:"paths"`
}

// Report is the whole scan outcome: the duplicate groups plus the
// summary counters. It renders as text for people and as JSON for
// scripts.
type Report struct {
	Root             string   `json:"root"`
	Groups           []Group  `json:"groups"`
	GroupCount       int      `json:"duplicate_groups"`
	FilesScanned     int64    `json:"files_scanned"`
	CandidatesHashed int64    `json:"candidates_hashed"`
	TotalWastedBytes int64    `json:"total_wasted_bytes"`
	TotalWastedHuman string   `json:"total_wasted"`
	SkippedSymlinks  int64    `json:"skipped_symlinks"`
	SkippedEmpty     int64    `json:"skipped_empty_files"`
	SkippedSpecial   int64    `json:"skipped_special_files"`
	ChangedFiles     int64    `json:"changed_during_scan"`
	ReadErrors       int64    `json:"read_errors"`
	ErrorSample      []string `json:"error_sample,omitempty"`
}

// buildGroups turns size → hash → paths into sorted groups, keeping
// only hashes with two or more surviving files. It is a pure
// function of its input so the ordering and the wasted-space math
// can be tested exactly.
func buildGroups(grouped map[int64]map[[sha256.Size]byte][]string) []Group {
	groups := make([]Group, 0)
	for size, byHash := range grouped {
		for hash, paths := range byHash {
			if len(paths) < 2 {
				continue
			}
			sorted := append([]string(nil), paths...)
			sort.Strings(sorted)
			wasted := size * int64(len(sorted)-1)
			groups = append(groups, Group{
				Size:        size,
				SizeHuman:   formatBytes(size),
				Hash:        hex.EncodeToString(hash[:]),
				Copies:      len(sorted),
				WastedBytes: wasted,
				WastedHuman: formatBytes(wasted),
				Paths:       sorted,
			})
		}
	}
	sortGroups(groups)
	return groups
}

// sortGroups orders groups so the biggest waste comes first: wasted
// bytes descending, then file size descending, then first path
// ascending. A group's first path is unique across groups (a file
// belongs to exactly one group), so this order is total and the
// report is deterministic.
func sortGroups(groups []Group) {
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.WastedBytes != b.WastedBytes {
			return a.WastedBytes > b.WastedBytes
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.Paths[0] < b.Paths[0]
	})
}

// buildReport assembles the final report from the grouped hashes and
// the scan counters.
func buildReport(root string, grouped map[int64]map[[sha256.Size]byte][]string, stats *scanStats) *Report {
	groups := buildGroups(grouped)
	var total int64
	for _, g := range groups {
		total += g.WastedBytes
	}
	return &Report{
		Root:             root,
		Groups:           groups,
		GroupCount:       len(groups),
		FilesScanned:     stats.FilesScanned,
		CandidatesHashed: stats.CandidatesHashed,
		TotalWastedBytes: total,
		TotalWastedHuman: formatBytes(total),
		SkippedSymlinks:  stats.SkippedSymlinks,
		SkippedEmpty:     stats.SkippedEmpty,
		SkippedSpecial:   stats.SkippedSpecial,
		ChangedFiles:     stats.ChangedFiles,
		ReadErrors:       stats.ReadErrors,
		ErrorSample:      stats.ErrorSample,
	}
}

// renderText renders the human-readable report.
func (r *Report) renderText() string {
	var b strings.Builder
	b.WriteString("Duplicate finder report\n")
	fmt.Fprintf(&b, "Root: %s\n\n", r.Root)

	if r.GroupCount == 0 {
		b.WriteString("No duplicate files found.\n\n")
	} else {
		for i, g := range r.Groups {
			fmt.Fprintf(&b, "Group %d: %d copies of %s each, wasted %s\n",
				i+1, g.Copies, g.SizeHuman, g.WastedHuman)
			fmt.Fprintf(&b, "  sha256: %s\n", g.Hash)
			for _, p := range g.Paths {
				fmt.Fprintf(&b, "  %s\n", p)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("Summary\n")
	fmt.Fprintf(&b, "  Files scanned:       %d\n", r.FilesScanned)
	fmt.Fprintf(&b, "  Candidates hashed:   %d\n", r.CandidatesHashed)
	fmt.Fprintf(&b, "  Duplicate groups:    %d\n", r.GroupCount)
	fmt.Fprintf(&b, "  Total wasted space:  %s\n", r.TotalWastedHuman)
	fmt.Fprintf(&b, "  Skipped symlinks:    %d\n", r.SkippedSymlinks)
	fmt.Fprintf(&b, "  Skipped empty files: %d\n", r.SkippedEmpty)
	fmt.Fprintf(&b, "  Skipped special:     %d\n", r.SkippedSpecial)
	fmt.Fprintf(&b, "  Changed during scan: %d\n", r.ChangedFiles)
	fmt.Fprintf(&b, "  Read errors:         %d\n", r.ReadErrors)
	if len(r.ErrorSample) > 0 {
		b.WriteString("  Unreadable paths (first few):\n")
		for _, p := range r.ErrorSample {
			fmt.Fprintf(&b, "    %s\n", p)
		}
	}
	return b.String()
}
