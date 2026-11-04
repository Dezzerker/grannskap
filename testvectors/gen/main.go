// gen writes testvectors/vectors.json from a fixed seed, so the vectors are reproducible.
//
//	go run ./testvectors/gen > testvectors/vectors.json
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/Dezzerker/grannskap"
)

func main() {
	seed := sha256.Sum256([]byte("grannskap testvectors v1"))
	sk := ed25519.NewKeyFromSeed(seed[:])
	pub := base64.StdEncoding.EncodeToString(sk.Public().(ed25519.PublicKey))
	priv := base64.StdEncoding.EncodeToString(sk)
	type kase struct {
		Name      string `json:"name"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		Timestamp int64  `json:"timestamp"`
		Body      string `json:"body"`
		Canonical string `json:"canonical"`
		BodySHA   string `json:"body_sha256"`
		Signature string `json:"signature"`
	}
	in := []kase{
		{Name: "discovery-not-signed-but-status-get", Method: "GET", Path: grannskap.PathRequest + "/12", Timestamp: 1762228800, Body: ""},
		{Name: "request-post", Method: "POST", Path: grannskap.PathRequest, Timestamp: 1762228800, Body: `{"message":"Hej!","services_wanted":["wiki"],"token_for_you":"abcdefghijklmnopqrstuvwxyz0123456789ABCDEF"}`},
		{Name: "wiki-get-with-token", Method: "GET", Path: grannskap.PathWiki, Timestamp: 1762228860, Body: ""},
		{Name: "feedback-post-utf8", Method: "POST", Path: grannskap.PathFeedback, Timestamp: 1762228920, Body: `{"text":"Snyggt årshjul! ÅÄÖ åäö"}`},
		{Name: "status-get", Method: "get", Path: grannskap.PathStatus, Timestamp: 1762228980, Body: ""},
	}
	for i := range in {
		c := &in[i]
		c.Canonical = string(grannskap.Canonical(c.Method, c.Path, c.Timestamp, []byte(c.Body)))
		sum := sha256.Sum256([]byte(c.Body))
		c.BodySHA = hex.EncodeToString(sum[:])
		sig, err := grannskap.Sign(priv, c.Method, c.Path, c.Timestamp, []byte(c.Body))
		if err != nil {
			panic(err)
		}
		c.Signature = sig
	}
	out := map[string]any{
		"note":        "Fast nyckelpar för tester. Signaturen är ed25519 över canonical (METOD, SÖKVÄG, TIDSSTÄMPEL och hex(sha256(kropp)) åtskilda av radbrytning). Använd ALDRIG den här nyckeln på ett riktigt hus.",
		"public_key":  pub,
		"private_key": priv,
		"cases":       in,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}
