package main

import "testing"

func TestParseSize(t *testing.T) {
	valid := []struct {
		text string
		want int64
	}{
		{"0", 0},
		{"1", 1},
		{"100", 100},
		{"100B", 100},
		{"100KB", 100_000},
		{"100KiB", 102_400},
		{"1MiB", 1_048_576},
		{"1.5GiB", 1_610_612_736},
		{"2tb", 2_000_000_000_000},
		{" 10 MB ", 10_000_000},
		{"1K", 1024},
		{"3M", 3 * 1024 * 1024},
	}
	for _, tc := range valid {
		got, err := ParseSize(tc.text)
		if err != nil {
			t.Errorf("ParseSize(%q): unexpected error %v", tc.text, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}

	invalid := []string{"", "abc", "-5", "10XB", "1.5", "1.5.2KB", "KB", "99999999999999999999TB"}
	for _, text := range invalid {
		if _, err := ParseSize(text); err == nil {
			t.Errorf("ParseSize(%q): expected an error, got nil", text)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.00 KiB (1024 bytes)"},
		{2048, "2.00 KiB (2048 bytes)"},
		{5 * 1024 * 1024, "5.00 MiB (5242880 bytes)"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.bytes); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
