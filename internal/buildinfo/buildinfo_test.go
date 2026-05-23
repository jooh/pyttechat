package buildinfo

import "testing"

func TestSummaryDefaults(t *testing.T) {
	got := Summary()
	want := "version=dev commit=unknown date=unknown"

	if got != want {
		t.Fatalf("Summary() = %q, want %q", got, want)
	}
}

func TestSnapshotDefaults(t *testing.T) {
	got := Snapshot()
	want := Info{
		Version: "dev",
		Commit:  "unknown",
		Date:    "unknown",
	}

	if got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}
