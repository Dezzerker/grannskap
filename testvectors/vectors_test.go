package testvectors

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Dezzerker/grannskap"
)

// vectors.json holds a fixed key pair and signed calls. Any implementation, in any language,
// should produce exactly these signatures from the same inputs; and verify them.
type vectors struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Cases      []struct {
		Name      string `json:"name"`
		Method    string `json:"method"`
		Path      string `json:"path"`
		Timestamp int64  `json:"timestamp"`
		Body      string `json:"body"`
		Canonical string `json:"canonical"`
		BodySHA   string `json:"body_sha256"`
		Signature string `json:"signature"`
	} `json:"cases"`
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Cases {
		canon := grannskap.Canonical(c.Method, c.Path, c.Timestamp, []byte(c.Body))
		if string(canon) != c.Canonical {
			t.Errorf("%s: canonical\n got %q\nwant %q", c.Name, canon, c.Canonical)
		}
		sig, err := grannskap.Sign(v.PrivateKey, c.Method, c.Path, c.Timestamp, []byte(c.Body))
		if err != nil || sig != c.Signature {
			t.Errorf("%s: signature %q want %q (%v)", c.Name, sig, c.Signature, err)
		}
		// Verify with a clock inside the window of the stored timestamp.
		if err := grannskap.Verify(v.PublicKey, c.Method, c.Path, c.Timestamp, []byte(c.Body), c.Signature, time.Unix(c.Timestamp, 0)); err != nil {
			t.Errorf("%s: verify: %v", c.Name, err)
		}
		if _, err := hex.DecodeString(c.BodySHA); err != nil || len(c.BodySHA) != 64 {
			t.Errorf("%s: body sha", c.Name)
		}
	}
}
