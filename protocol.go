// Package grannskap is the wire protocol between households' home sites: the discovery document
// every house publishes, the request/approval handshake, signed calls, and the small read-only
// services (wiki, household, friends, feedback). Everything here is pure: no database, no HTTP
// server. The web layer serves it and the Client calls other houses.
package grannskap

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Version of the protocol in the discovery document and the API path.
const Version = "1"

// Kinds of houses. A house is any place with its own site and AI: a household, a building that
// belongs to one (garage, hydro plant), a business, an association, or something else.
const (
	KindHushall  = "hushall"
	KindByggnad  = "byggnad"
	KindForetag  = "foretag"
	KindForening = "forening"
	KindAnnat    = "annat"
)

// KindInfo is a kind with its label.
type KindInfo struct{ Key, Label string }

// Kinds lists the house kinds with labels.
var Kinds = []KindInfo{{KindHushall, "Hushåll"}, {KindByggnad, "Byggnad (tillhör ett hushåll)"}, {KindForetag, "Företag"}, {KindForening, "Förening"}, {KindAnnat, "Annat"}}

// ValidKind reports whether k is a house kind; "" counts as hushall.
func ValidKind(k string) bool {
	switch k {
	case "", KindHushall, KindByggnad, KindForetag, KindForening, KindAnnat:
		return true
	}
	return false
}

// Service keys a house can announce. Schema and bokning are reserved for associations and
// businesses (times, schedules, bookable slots) and not implemented here yet.
const (
	ServiceWiki     = "wiki"
	ServiceHushall  = "hushall"
	ServiceFeedback = "feedback"
	ServiceVanner   = "vanner"
	ServiceRecept   = "recept"
	ServiceSchema   = "schema"
	ServiceBokning  = "bokning"
)

// Paths under a house (relative to its address).
const (
	PathDiscovery = "/.well-known/grannskap.json"
	PathRequest   = "/api/grannskap/v1/forfragan"
	PathWiki      = "/api/grannskap/v1/wiki"
	PathHousehold = "/api/grannskap/v1/hushall"
	PathFeedback  = "/api/grannskap/v1/feedback"
	PathFriends   = "/api/grannskap/v1/vanner"
)

// Headers on signed calls.
const (
	HeaderHouse     = "X-Grannskap-House"
	HeaderTimestamp = "X-Grannskap-Timestamp"
	HeaderSignature = "X-Grannskap-Signature"
)

// MaxSkew is how old (or early) a signed call may be.
const MaxSkew = 5 * time.Minute

// Discovery is /.well-known/grannskap.json.
type Discovery struct {
	Grannskap    string            `json:"grannskap"`
	Kind         string            `json:"kind"`
	Parent       string            `json:"parent,omitempty"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	URL          string            `json:"url"`
	PublicKey    string            `json:"public_key"`
	Discoverable bool              `json:"discoverable"`
	Services     []Service         `json:"services"`
	Endpoints    map[string]string `json:"endpoints"`
	RateLimit    int               `json:"rate_limit_per_minute"`
	Docs         string            `json:"docs"`
	Version      string            `json:"version,omitempty"`                     // the site build, changes on every deploy
	WikiUpdated  string            `json:"wiki_updated,omitempty"`                // RFC 3339, when the wiki last changed
	Status       string            `json:"status,omitempty"`                      // what the house is up to right now
	Time         string            `json:"time,omitempty"`                        // RFC 3339, when this document was built
	StatusEvery  int               `json:"status_min_interval_seconds,omitempty"` // how often a friend may ask /status
}

// Service is one announced capability.
type Service struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

// Request is the body of POST /forfragan.
type Request struct {
	Message        string   `json:"message"`
	ServicesWanted []string `json:"services_wanted"`
	TokenForYou    string   `json:"token_for_you"` // the asking house's token for the asked house to use
}

// RequestReply answers POST and GET /forfragan.
type RequestReply struct {
	ID          int64    `json:"id"`
	Status      string   `json:"status"` // inkommen | van | avslagen | pausad
	TokenForYou string   `json:"token_for_you,omitempty"`
	Services    []string `json:"services,omitempty"`
	Message     string   `json:"message,omitempty"`
}

// House is the short identity carried in answers.
type House struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind,omitempty"`
}

// WikiSection is one part of a house's wiki.
type WikiSection struct {
	Key   string   `json:"key"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// Wiki answers GET /wiki.
type Wiki struct {
	House    House         `json:"house"`
	Sections []WikiSection `json:"sections"`
}

// Member is one shared household member; empty fields were not shared.
type Member struct {
	Name        string `json:"name,omitempty"`
	Role        string `json:"role,omitempty"`     // vuxen | barn
	Age         int    `json:"age,omitempty"`      // 0 = not shared
	Birthday    string `json:"birthday,omitempty"` // MM-DD, or YYYY-MM-DD when age is shared too
	WishlistURL string `json:"wishlist_url,omitempty"`
}

// Household answers GET /hushall.
type Household struct {
	House   House    `json:"house"`
	Members []Member `json:"members"`
}

// Friend is one entry in GET /vanner (friends of a friend).
type Friend struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description,omitempty"`
}

// Friends answers GET /vanner.
type Friends struct {
	House   House    `json:"house"`
	Friends []Friend `json:"friends"`
}

// Feedback is the body of POST /feedback.
type Feedback struct {
	Text string `json:"text"`
}

// Error is the JSON error answer.
type Error struct {
	Error string `json:"error"`
}

// NormalizeAddress validates a house address: https (http allowed for localhost/testing), a
// host, no path, no trailing slash.
func NormalizeAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("adress saknas")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("adressen går inte att tolka")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || strings.HasSuffix(u.Hostname(), ".test"))) {
		return "", errors.New("adressen måste börja med https://")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("adressen får bara vara värdnamnet")
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" {
		return "", errors.New("adressen får inte ha någon sökväg efter värdnamnet")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// NewToken makes a 32-byte random token, URL-safe.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewKeyPair makes an ed25519 key pair, both base64.
func NewKeyPair() (pub, priv string, err error) {
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(sk), nil
}

// Canonical is the string that gets signed: method, path, timestamp and the body's sha256.
func Canonical(method, path string, ts int64, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.ToUpper(method) + "\n" + path + "\n" + strconv.FormatInt(ts, 10) + "\n" + hex.EncodeToString(sum[:]))
}

// Sign signs a call with the house's private key (base64) and returns the signature (base64).
func Sign(privB64, method, path string, ts int64, body []byte) (string, error) {
	sk, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(sk) != ed25519.PrivateKeySize {
		return "", errors.New("ogiltig privat nyckel")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(sk), Canonical(method, path, ts, body))), nil
}

// Verify checks a signature against a public key (base64), and that the timestamp is fresh.
func Verify(pubB64, method, path string, ts int64, body []byte, sigB64 string, now time.Time) error {
	pk, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return errors.New("ogiltig publik nyckel")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return errors.New("ogiltig signatur")
	}
	d := now.Unix() - ts
	if d > int64(MaxSkew/time.Second) || d < -int64(MaxSkew/time.Second) {
		return errors.New("tidsstämpeln är för gammal eller ligger i framtiden")
	}
	if !ed25519.Verify(ed25519.PublicKey(pk), Canonical(method, path, ts, body), sig) {
		return errors.New("signaturen stämmer inte")
	}
	return nil
}

// ValidService reports whether a service key is one we know.
func ValidService(k string) bool {
	switch k {
	case ServiceWiki, ServiceHushall, ServiceFeedback, ServiceVanner, ServiceRecept, ServiceSchema, ServiceBokning:
		return true
	}
	return false
}

// PathStatus is the status question (GET, signed with a friend token) and the status push
// (POST, same body) between friends.
const PathStatus = "/api/grannskap/v1/status"

// Status is what a house says about itself when asked, or pushes to its friends. The asked
// house decides how often it answers: MinInterval tells the asker how often it may ask.
type Status struct {
	Online      bool   `json:"online"`
	Version     string `json:"version,omitempty"`
	Status      string `json:"status,omitempty"`
	WikiUpdated string `json:"wiki_updated,omitempty"`
	Time        string `json:"time"`
	MinInterval int    `json:"min_interval_seconds,omitempty"`
}

// TooSoon is the 429 answer to a status question asked too often.
type TooSoon struct {
	Error      string `json:"error"`
	RetryAfter int    `json:"retry_after_seconds"`
}
