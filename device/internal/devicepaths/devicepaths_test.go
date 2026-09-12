package devicepaths

import "testing"

func has(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestTheCurrentPathWins(t *testing.T) {
	got := pick("controller.json", "/new", []string{"/old"},
		has("/new/controller.json", "/old/controller.json"))
	if got != "/new/controller.json" {
		t.Fatalf("got %s — a migrated file must never be read from the stale copy", got)
	}
}

func TestTheLegacyPathIsReadWhenTheCurrentOneIsAbsent(t *testing.T) {
	// The upgrade case, and the whole point: the file is on the device, it
	// is just where the PREVIOUS firmware put it.
	got := pick("controller.json", "/new", []string{"/old"}, has("/old/controller.json"))
	if got != "/old/controller.json" {
		t.Fatalf("got %s — the remembered controller is lost on every upgrade", got)
	}
}

func TestAbsenceEverywhereNamesTheCurrentPath(t *testing.T) {
	// First boot. A caller logging "not found" should name where a reader
	// would look, not the last directory tried.
	got := pick("state.json", "/new", []string{"/old"}, has())
	if got != "/new/state.json" {
		t.Fatalf("got %s, want the current path", got)
	}
}

func TestWritesAlwaysGoToTheCurrentDirectory(t *testing.T) {
	// A write is what migrates the file, so it must not follow the read.
	if got := Write("state.json"); got != CurrentDir+"/state.json" {
		t.Fatalf("got %s", got)
	}
}

func TestTheLegacyDirectoryIsStillListed(t *testing.T) {
	// Deleting this strands every device that has not yet written the file
	// again — including one whose only remaining state is the endpoint it
	// needs in order to be reachable at all.
	found := false
	for _, d := range LegacyDirs {
		if d == "/data/local/etc/echomuse" {
			found = true
		}
	}
	if !found {
		t.Fatal("the pre-rename directory is no longer read")
	}
}
