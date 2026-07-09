package main

import "testing"

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"1.2.3", true},
		{"v1.2.3", true},
		{"latest", false},
		{"1.2.3-dev", false},
		{"", false},
		{"1.2", false},
	}
	for _, c := range cases {
		if _, ok := parseSemver(c.in); ok != c.ok {
			t.Errorf("parseSemver(%q) ok=%v, want %v", c.in, ok, c.ok)
		}
	}
}

func TestSemverLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.3", "1.2.4", true},
		{"1.2.3", "1.2.3", false},
		{"1.2.10", "1.2.9", false},
		{"0.9.9", "1.0.0", true},
		{"latest", "1.0.0", false}, // non-semver never compares
		{"1.0.0", "latest", false},
	}
	for _, c := range cases {
		if got := semverLess(c.a, c.b); got != c.want {
			t.Errorf("semverLess(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalizeMAC(t *testing.T) {
	if normalizeMAC("AA-BB-CC-DD-EE-FF") != "aa:bb:cc:dd:ee:ff" {
		t.Error("normalizeMAC failed on dashes/case")
	}
	if !isMAC("aa:bb:cc:dd:ee:ff") {
		t.Error("isMAC rejected valid MAC")
	}
	if isMAC("pending") || isMAC("") || isMAC("aa:bb:cc:dd:ee") {
		t.Error("isMAC accepted invalid identity")
	}
}
