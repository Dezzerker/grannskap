package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dezzerker/grannskap"
)

// TestWholeHandshake runs a fake house against an in-process Testhuset: request, status, wiki
// with token, and Testhuset reading the fake house's wiki back.
func TestWholeHandshake(t *testing.T) {
	pub, priv, _ := grannskap.NewKeyPair()
	var fake *httptest.Server
	var tokenSeen string
	fake = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case grannskap.PathDiscovery:
			json.NewEncoder(w).Encode(grannskap.Discovery{Grannskap: grannskap.Version, Kind: grannskap.KindForening, Name: "Låtsashuset", URL: fake.URL, PublicKey: pub,
				Services: []grannskap.Service{{Key: "wiki", Version: 1}}})
		case grannskap.PathWiki:
			tokenSeen = strings.TrimPrefix(r.Header.Get("Authorization"), "Grannskap ")
			json.NewEncoder(w).Encode(grannskap.Wiki{House: grannskap.House{Name: "Låtsashuset", URL: fake.URL},
				Sections: []grannskap.WikiSection{{Key: "a", Title: "A", Body: "Hej <b>du</b>"}, {Key: "b", Title: "B", Body: "Två"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()

	s := &server{path: filepath.Join(t.TempDir(), "data.json"), url: "http://127.0.0.1:1", hits: map[string][]time.Time{}}
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	s.client = grannskap.NewClient(s.url, s.d.PrivateKey)
	th := httptest.NewServer(s.routes())
	defer th.Close()

	c := grannskap.NewClient(fake.URL, priv)
	ctx := context.Background()
	d, err := c.FetchDiscovery(ctx, th.URL)
	if err != nil || d.Name != "Testhuset" {
		t.Fatalf("discovery: %+v %v", d, err)
	}
	rep, err := c.SendRequest(ctx, th.URL, grannskap.Request{Message: "Hej!", ServicesWanted: []string{"wiki"}, TokenForYou: "token-for-testhuset-0123456789"})
	if err != nil || rep.Status != "van" || rep.TokenForYou == "" {
		t.Fatalf("request: %+v %v", rep, err)
	}
	st, err := c.RequestStatus(ctx, th.URL, rep.ID)
	if err != nil || st.TokenForYou != rep.TokenForYou {
		t.Fatalf("status: %+v %v", st, err)
	}
	wk, err := c.GetWiki(ctx, th.URL, rep.TokenForYou)
	if err != nil || len(wk.Sections) != 4 {
		t.Fatalf("wiki with token: %d sections, %v", len(wk.Sections), err)
	}
	if _, err := c.GetWiki(ctx, th.URL, "fel-token"); err == nil {
		t.Fatal("wrong token must fail")
	}
	// Testhuset reads our wiki after a short delay.
	deadline := time.Now().Add(10 * time.Second)
	for {
		h := s.houseByAddress(fake.URL)
		if h != nil && h.Steps.WikiGiven {
			if h.WikiCount != 2 || h.Steps.Done() != 4 || h.Kind != grannskap.KindForening || tokenSeen != "token-for-testhuset-0123456789" {
				t.Fatalf("house after test: %+v tokenSeen=%q", h, tokenSeen)
			}
			if strings.Contains(h.Wiki[0].Body, "<b>") {
				t.Fatalf("foreign wiki not cleaned: %q", h.Wiki[0].Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("testhuset never read our wiki: %+v", h)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Pages render.
	for _, p := range []string{"/", "/hus/1"} {
		resp, err := http.Get(th.URL + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("page %s: %v", p, err)
		}
		resp.Body.Close()
	}
}
