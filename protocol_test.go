package grannskap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAddress(t *testing.T) {
	good := map[string]string{
		"hem.hexance.se":              "https://hem.hexance.se",
		"https://Hem.Hexance.se/":     "https://hem.hexance.se",
		"http://localhost:8082":       "http://localhost:8082",
		"https://hus.example.se:8443": "https://hus.example.se:8443",
	}
	for in, want := range good {
		if got, err := NormalizeAddress(in); err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "http://hus.example.se", "https://hus.example.se/api", "https://a:b@hus.example.se", "https://hus.example.se?x=1"} {
		if _, err := NormalizeAddress(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSignVerify(t *testing.T) {
	pub, priv, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"message":"hej"}`)
	now := time.Now()
	sig, err := Sign(priv, "post", PathRequest, now.Unix(), body)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, "POST", PathRequest, now.Unix(), body, sig, now); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(pub, "POST", PathRequest, now.Unix(), []byte(`{"message":"hej!"}`), sig, now); err == nil {
		t.Fatal("changed body must fail")
	}
	if err := Verify(pub, "POST", PathWiki, now.Unix(), body, sig, now); err == nil {
		t.Fatal("changed path must fail")
	}
	if err := Verify(pub, "POST", PathRequest, now.Unix(), body, sig, now.Add(10*time.Minute)); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	other, _, _ := NewKeyPair()
	if err := Verify(other, "POST", PathRequest, now.Unix(), body, sig, now); err == nil {
		t.Fatal("other key must fail")
	}
	tok, _ := NewToken()
	if len(tok) < 40 || strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token: %q", tok)
	}
}

// TestClientRoundTrip runs a fake house and checks that the client signs, sends and decodes.
func TestClientRoundTrip(t *testing.T) {
	pub, priv, _ := NewKeyPair()
	var seen http.Header
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case PathDiscovery:
			json.NewEncoder(w).Encode(Discovery{Grannskap: Version, Name: "Testhuset", URL: "http://" + r.Host, PublicKey: pub,
				Services: []Service{{Key: ServiceWiki, Version: 1}}})
		case PathRequest:
			seen = r.Header.Clone()
			b := make([]byte, 4096)
			n, _ := r.Body.Read(b)
			seenBody = string(b[:n])
			json.NewEncoder(w).Encode(RequestReply{ID: 7, Status: "inkommen"})
		case PathWiki:
			if r.Header.Get("Authorization") != "Grannskap tok" {
				w.WriteHeader(401)
				json.NewEncoder(w).Encode(Error{Error: "okänt hus"})
				return
			}
			json.NewEncoder(w).Encode(Wiki{House: House{Name: "Testhuset"}, Sections: []WikiSection{{Key: "oversikt", Title: "Översikt", Body: "Hej"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	// The fake server listens on 127.0.0.1, which NormalizeAddress allows over http.
	c := NewClient("http://127.0.0.1:1", priv)
	d, err := c.FetchDiscovery(context.Background(), srv.URL)
	if err != nil || d.Name != "Testhuset" || d.PublicKey != pub {
		t.Fatalf("discovery: %+v %v", d, err)
	}
	rep, err := c.SendRequest(context.Background(), srv.URL, Request{Message: "hej", ServicesWanted: []string{ServiceWiki}, TokenForYou: "t1"})
	if err != nil || rep.ID != 7 {
		t.Fatalf("request: %+v %v", rep, err)
	}
	ts := seen.Get(HeaderTimestamp)
	var tsn int64
	for _, ch := range ts {
		tsn = tsn*10 + int64(ch-'0')
	}
	if err := Verify(pub, "POST", PathRequest, tsn, []byte(seenBody), seen.Get(HeaderSignature), time.Now()); err != nil {
		t.Fatalf("the call was not signed correctly: %v", err)
	}
	if seen.Get(HeaderHouse) != "http://127.0.0.1:1" {
		t.Fatalf("house header: %q", seen.Get(HeaderHouse))
	}
	if _, err := c.GetWiki(context.Background(), srv.URL, ""); err == nil || !strings.Contains(err.Error(), "okänt hus") {
		t.Fatalf("expected the server's error text, got %v", err)
	}
	wk, err := c.GetWiki(context.Background(), srv.URL, "tok")
	if err != nil || len(wk.Sections) != 1 {
		t.Fatalf("wiki: %+v %v", wk, err)
	}
}
