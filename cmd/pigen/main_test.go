package main

import "testing"

// First 100 decimal digits of pi (the leading 3 plus 99 fractional digits),
// no decimal point — exactly what the spigot emits, one digit per next().
const piRef = "3141592653589793238462643383279502884197169399375105820974944592307816406286208998628034825342117067"

func TestSpigotDigits(t *testing.T) {
	s := newSpigot()
	got := make([]byte, len(piRef))
	for i := range got {
		got[i] = s.next()
	}
	if string(got) != piRef {
		t.Fatalf("first %d pi digits wrong:\n got %s\nwant %s", len(piRef), got, piRef)
	}
}

func TestUDPPayload(t *testing.T) {
	p := udpPayload(config{udpDigits: 10})
	if want := 11; len(p) != want { // 10 digits + the decimal point after the first
		t.Fatalf("len = %d, want %d (got %q)", len(p), want, p)
	}
	if string(p[:2]) != "3." {
		t.Fatalf("payload should start with %q, got %q", "3.", p)
	}

	if legacy := udpPayload(config{legacyPi: true}); string(legacy) != "3" {
		t.Fatalf("legacy payload = %q, want %q", legacy, "3")
	}
}
