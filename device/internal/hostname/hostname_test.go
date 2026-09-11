package hostname

import "strings"

import "testing"

func TestARealSerialBecomesARealName(t *testing.T) {
	got := For("G090L91180250AN1")
	if got != "revoice-g090l91180250an1" {
		t.Fatalf("got %q", got)
	}
}

func TestTheNameIsAlwaysALegalDNSLabel(t *testing.T) {
	// The failure this guards is silent: an illegal label is published
	// without complaint and simply never resolves for anybody.
	cases := []string{
		"G090L91180250AN1", "", "   ", "---", "__", "...",
		"-leading", "trailing-", "with spaces", "MiXeD.Case_01",
		"ünïcödé", "!!!", strings.Repeat("X", 200),
	}
	for _, in := range cases {
		got := For(in)
		if got == "" {
			t.Errorf("%q produced an empty hostname", in)
			continue
		}
		if len(got) > maxLen {
			t.Errorf("%q produced %d chars, over the DNS label limit", in, len(got))
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("%q produced %q, which starts or ends with a hyphen", in, got)
		}
		if strings.Contains(got, "--") {
			t.Errorf("%q produced %q, with a doubled hyphen", in, got)
		}
		for _, r := range got {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Errorf("%q produced %q, containing %q", in, got, r)
				break
			}
		}
	}
}

func TestAnUnusableSerialStillNamesTheDevice(t *testing.T) {
	// Never "localhost", and never empty: both are names that cannot work
	// for any client, which is the whole reason this package exists.
	for _, in := range []string{"", "!!!", "...", "___"} {
		if got := For(in); got != Prefix {
			t.Errorf("For(%q) = %q, want the bare prefix", in, got)
		}
	}
}

func TestTheNameIsStableForOneSerial(t *testing.T) {
	// mDNS caches a hostname, so a name that moved between boots would
	// leave every client holding a stale one until its TTL expired.
	a, b := For("G090L91180250AN1"), For("G090L91180250AN1")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestDifferentSerialsGetDifferentNames(t *testing.T) {
	if For("G090L91180250AN1") == For("G090L91180250AN2") {
		t.Fatal("two serials collapsed onto one hostname")
	}
}
