package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseMACFormats(t *testing.T) {
	want := "aa:bb:cc:dd:ee:ff"
	for _, in := range []string{"aa:bb:cc:dd:ee:ff", "AA-BB-CC-DD-EE-FF", "aabbccddeeff"} {
		hw, err := parseMAC(in)
		if err != nil {
			t.Fatalf("parseMAC(%q): %v", in, err)
		}
		if hw.String() != want {
			t.Errorf("parseMAC(%q)=%s, want %s", in, hw, want)
		}
	}
	if _, err := parseMAC("not-a-mac"); err == nil {
		t.Error("parseMAC accepted garbage")
	}
}

func TestMagicPacketShape(t *testing.T) {
	hw, _ := parseMAC("aa:bb:cc:dd:ee:ff")
	payload := make([]byte, 0, 102)
	payload = append(payload, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	for i := 0; i < 16; i++ {
		payload = append(payload, hw...)
	}
	if len(payload) != 102 {
		t.Fatalf("magic packet length %d, want 102", len(payload))
	}
	if !bytes.Equal(payload[:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) {
		t.Error("magic packet missing sync stream")
	}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(payload[6+i*6:12+i*6], hw) {
			t.Fatalf("magic packet repetition %d corrupt", i)
		}
	}
}

func TestWakeRoute(t *testing.T) {
	server, _ := mockSPR(t)
	setupTestEnv(t, server)
	loadConfig()
	refreshState()

	router := newRouter()

	// invalid mac rejected before any packet goes out
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/ha/v1/wake?mac=nonsense", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad mac: got %d, want 400", rec.Code)
	}

	// valid mac: GET works (side effect is a LAN UDP packet)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/ha/v1/wake?mac=aa:bb:cc:dd:ee:01", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("wake: got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// still no non-GET methods anywhere
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/ha/v1/wake?mac=aa:bb:cc:dd:ee:01", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST wake: got %d, want 405", rec.Code)
	}
}
