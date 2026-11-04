// Testhuset is a small Grannskap house that other houses can test against the first time they
// run the protocol. It approves every request at once, lets the caller read its wiki with the
// token it got, then reads the caller's wiki with the token the caller gave, and shows on its
// front page how far each house came. Everything is kept in one JSON file.
//
//	testhus -addr :8084 -url https://testhus.hexance.se -data /var/lib/grannskap-testhus/data.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dezzerker/grannskap"
)

const repo = "https://github.com/Dezzerker/grannskap"

// house is one house that tested against us.
type house struct {
	ID        int64                   `json:"id"`
	Address   string                  `json:"address"`
	Name      string                  `json:"name"`
	Kind      string                  `json:"kind"`
	Desc      string                  `json:"description"`
	PublicKey string                  `json:"public_key"`
	TokenIn   string                  `json:"token_in"`  // they show this to us
	TokenOut  string                  `json:"token_out"` // we show this to them
	Message   string                  `json:"message"`
	FirstSeen time.Time               `json:"first_seen"`
	LastSeen  time.Time               `json:"last_seen"`
	Steps     steps                   `json:"steps"`
	WikiCount int                     `json:"wiki_count"`
	WikiAt    time.Time               `json:"wiki_at"`
	WikiError string                  `json:"wiki_error"`
	Wiki      []grannskap.WikiSection `json:"wiki"`
	Feedback  []string                `json:"feedback"`
	Status    string                  `json:"status"`
}

// steps are ticked as the house gets through the test.
type steps struct {
	Request   bool `json:"forfragan"`   // 1: signed request accepted
	StatusAsk bool `json:"status"`      // 2: asked for request status
	WikiRead  bool `json:"wiki_last"`   // 3: read our wiki with its token
	WikiGiven bool `json:"wiki_hamtad"` // 4: we could read its wiki
	Health    bool `json:"halsa"`       // extra: asked /status or pushed one
}

func (s steps) Done() int {
	n := 0
	for _, b := range []bool{s.Request, s.StatusAsk, s.WikiRead, s.WikiGiven} {
		if b {
			n++
		}
	}
	return n
}

type logEntry struct {
	At     time.Time `json:"at"`
	House  string    `json:"house"`
	Path   string    `json:"path"`
	Status int       `json:"status"`
	Note   string    `json:"note,omitempty"`
}

type data struct {
	PublicKey  string     `json:"public_key"`
	PrivateKey string     `json:"private_key"`
	NextID     int64      `json:"next_id"`
	Houses     []*house   `json:"houses"`
	Log        []logEntry `json:"log"`
	Calls      int64      `json:"calls"`
}

type server struct {
	mu     sync.Mutex
	d      data
	path   string
	url    string
	client *grannskap.Client
	tmpl   *template.Template
	hits   map[string][]time.Time
}

func main() {
	addr := flag.String("addr", ":8084", "lyssna på")
	url := flag.String("url", "https://testhus.hexance.se", "husets adress utåt")
	path := flag.String("data", "testhus-data.json", "datafil")
	flag.Parse()
	s := &server{path: *path, url: strings.TrimRight(*url, "/"), hits: map[string][]time.Time{}}
	if err := s.load(); err != nil {
		log.Fatal(err)
	}
	s.client = grannskap.NewClient(s.url, s.d.PrivateKey)
	s.tmpl = template.Must(template.New("page").Funcs(template.FuncMap{"since": since}).Parse(pageHTML))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /hus/{id}", s.handleHouse)
	mux.HandleFunc("GET "+grannskap.PathDiscovery, s.handleDiscovery)
	mux.HandleFunc("POST "+grannskap.PathRequest, s.handleRequest)
	mux.HandleFunc("GET "+grannskap.PathRequest+"/{id}", s.handleRequestStatus)
	mux.HandleFunc("GET "+grannskap.PathWiki, s.auth(true, s.handleWiki))
	mux.HandleFunc("GET "+grannskap.PathHousehold, s.auth(false, s.handleHousehold))
	mux.HandleFunc("GET "+grannskap.PathFriends, s.auth(false, s.handleFriends))
	mux.HandleFunc("POST "+grannskap.PathFeedback, s.auth(false, s.handleFeedback))
	mux.HandleFunc("GET "+grannskap.PathStatus, s.auth(false, s.handleStatus))
	mux.HandleFunc("POST "+grannskap.PathStatus, s.auth(false, s.handleStatusPush))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("testhuset lyssnar på %s som %s", *addr, s.url)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// ---- storage ----

func (s *server) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.d); err != nil {
			return err
		}
	}
	if s.d.PublicKey == "" {
		pub, priv, err := grannskap.NewKeyPair()
		if err != nil {
			return err
		}
		s.d.PublicKey, s.d.PrivateKey = pub, priv
		log.Println("nytt nyckelpar skapat")
	}
	if s.d.NextID == 0 {
		s.d.NextID = 1
	}
	return s.save()
}

func (s *server) save() error {
	raw, err := json.MarshalIndent(&s.d, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *server) logCall(address, path string, status int, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Calls++
	s.d.Log = append(s.d.Log, logEntry{At: time.Now(), House: address, Path: path, Status: status, Note: grannskap.Clean(note, 120)})
	if len(s.d.Log) > 300 {
		s.d.Log = s.d.Log[len(s.d.Log)-300:]
	}
	_ = s.save()
}

func (s *server) houseByToken(token string) *house {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.d.Houses {
		if token != "" && h.TokenIn == token {
			return h
		}
	}
	return nil
}

func (s *server) houseByAddress(address string) *house {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.d.Houses {
		if h.Address == address {
			return h
		}
	}
	return nil
}

func (s *server) houseByID(id int64) *house {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.d.Houses {
		if h.ID == id {
			return h
		}
	}
	return nil
}

// update runs fn under the lock and saves.
func (s *server) update(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
	_ = s.save()
}

func (s *server) allow(key string, max int, window time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var keep []time.Time
	for _, t := range s.hits[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= max {
		s.hits[key] = keep
		return false
	}
	s.hits[key] = append(keep, now)
	return true
}

// ---- helpers ----

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, grannskap.Error{Error: msg})
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

type signed struct {
	Address string
	Body    []byte
	TS      int64
	Sig     string
}

func readSigned(r *http.Request) (signed, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10+1))
	if err != nil || len(body) > 256<<10 {
		return signed{}, errors.New("kroppen är för stor eller gick inte att läsa")
	}
	addr, err := grannskap.NormalizeAddress(r.Header.Get(grannskap.HeaderHouse))
	if err != nil {
		return signed{}, errors.New("X-Grannskap-House saknas eller är ingen giltig adress")
	}
	ts, err := strconv.ParseInt(r.Header.Get(grannskap.HeaderTimestamp), 10, 64)
	if err != nil {
		return signed{}, errors.New("X-Grannskap-Timestamp saknas")
	}
	sig := r.Header.Get(grannskap.HeaderSignature)
	if sig == "" {
		return signed{}, errors.New("X-Grannskap-Signature saknas")
	}
	return signed{Address: addr, Body: body, TS: ts, Sig: sig}, nil
}

type ctxKey struct{}

type callInfo struct {
	House *house
	Body  []byte
}

func info(r *http.Request) callInfo {
	c, _ := r.Context().Value(ctxKey{}).(callInfo)
	return c
}

// auth checks token + signature for service calls. optional lets wiki answer anonymously.
func (s *server) auth(optional bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if authz == "" && optional {
			s.logCall(r.Header.Get(grannskap.HeaderHouse), r.URL.Path, 200, "utan token")
			next(w, r)
			return
		}
		token := strings.TrimPrefix(authz, "Grannskap ")
		if token == authz || token == "" {
			jsonErr(w, 401, "Authorization: Grannskap <token> saknas")
			return
		}
		h := s.houseByToken(token)
		if h == nil {
			s.logCall("", r.URL.Path, 401, "okänd token")
			jsonErr(w, 401, "okänt hus: skicka först en förfrågan och använd token_for_you ur svaret")
			return
		}
		c, err := readSigned(r)
		if err != nil {
			jsonErr(w, 400, err.Error())
			return
		}
		if c.Address != h.Address {
			jsonErr(w, 401, "X-Grannskap-House stämmer inte med token: token hör till "+h.Address)
			return
		}
		if err := grannskap.Verify(h.PublicKey, r.Method, r.URL.Path, c.TS, c.Body, c.Sig, time.Now()); err != nil {
			s.logCall(h.Address, r.URL.Path, 401, err.Error())
			jsonErr(w, 401, err.Error()+" (kontrollera canonical: METOD, SÖKVÄG, TIDSSTÄMPEL, hex(sha256(kropp)) åtskilda av \\n)")
			return
		}
		if !s.allow("house:"+h.Address, 30, time.Minute) {
			jsonErr(w, 429, "vänta: högst 30 anrop per minut")
			return
		}
		s.update(func() { h.LastSeen = time.Now() })
		s.logCall(h.Address, r.URL.Path, 200, "")
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, callInfo{House: h, Body: c.Body})))
	}
}

// ---- endpoints ----

func (s *server) discovery() grannskap.Discovery {
	return grannskap.Discovery{
		Grannskap: grannskap.Version, Kind: grannskap.KindAnnat, Name: "Testhuset", URL: s.url, PublicKey: s.d.PublicKey,
		Description:  "Ett testhus för Grannskap. Godkänner alla förfrågningar direkt, läser er wiki och visar hur långt ni kommit.",
		Discoverable: true,
		Services:     []grannskap.Service{{Key: "wiki", Version: 1}, {Key: "hushall", Version: 1}, {Key: "vanner", Version: 1}, {Key: "feedback", Version: 1}, {Key: "status", Version: 1}},
		Endpoints: map[string]string{"request": grannskap.PathRequest, "status": grannskap.PathRequest + "/{id}", "wiki": grannskap.PathWiki,
			"household": grannskap.PathHousehold, "friends": grannskap.PathFriends, "feedback": grannskap.PathFeedback, "health": grannskap.PathStatus},
		RateLimit: 30, Docs: repo, Version: "testhus-1", Status: "Öppet för tester dygnet runt.", Time: time.Now().UTC().Format(time.RFC3339), StatusEvery: 0,
	}
}

func (s *server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	s.logCall(clientIP(r), r.URL.Path, 200, "")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	jsonOut(w, 200, s.discovery())
}

func (s *server) handleRequest(w http.ResponseWriter, r *http.Request) {
	if !s.allow("ip:"+clientIP(r), 10, time.Minute) {
		jsonErr(w, 429, "vänta en minut")
		return
	}
	c, err := readSigned(r)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	var req grannskap.Request
	if err := json.Unmarshal(c.Body, &req); err != nil {
		jsonErr(w, 400, "kroppen är inte giltig JSON: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	d, err := s.client.FetchDiscovery(ctx, c.Address)
	if err != nil {
		s.logCall(c.Address, r.URL.Path, 400, err.Error())
		jsonErr(w, 400, "kunde inte läsa ert upptäcktsdokument: "+err.Error())
		return
	}
	if err := grannskap.Verify(d.PublicKey, r.Method, r.URL.Path, c.TS, c.Body, c.Sig, time.Now()); err != nil {
		s.logCall(c.Address, r.URL.Path, 401, err.Error())
		jsonErr(w, 401, err.Error()+" (nyckeln togs ur ert upptäcktsdokument)")
		return
	}
	tok := strings.TrimSpace(req.TokenForYou)
	if len(tok) < 16 || len(tok) > 200 {
		jsonErr(w, 400, "token_for_you saknas eller är för kort (minst 16 tecken, gärna 32 slumpade byte)")
		return
	}
	h := s.houseByAddress(c.Address)
	s.update(func() {
		if h == nil {
			h = &house{ID: s.d.NextID, Address: c.Address, FirstSeen: time.Now()}
			s.d.NextID++
			s.d.Houses = append(s.d.Houses, h)
		}
		if h.TokenIn == "" {
			h.TokenIn, _ = grannskap.NewToken()
		}
		h.Name, h.Kind, h.Desc, h.PublicKey, h.TokenOut = d.Name, d.Kind, d.Description, d.PublicKey, tok
		h.Message = grannskap.CleanText(req.Message, 2000)
		h.LastSeen = time.Now()
		h.Steps.Request = true
	})
	s.logCall(c.Address, r.URL.Path, 200, "förfrågan godkänd")
	go s.readTheirWiki(h)
	jsonOut(w, 200, s.reply(h))
}

func (s *server) reply(h *house) grannskap.RequestReply {
	return grannskap.RequestReply{ID: h.ID, Status: "van", TokenForYou: h.TokenIn, Services: []string{"wiki", "hushall", "vanner", "feedback", "status"},
		Message: "Testhuset godkänner alla. Steg kvar: fråga status (GET forfragan/{id}), läs vår wiki med er token, och se på " + s.url + " om vi kunde läsa er wiki."}
}

func (s *server) handleRequestStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	h := s.houseByID(id)
	if err != nil || h == nil {
		jsonErr(w, 404, "okänd förfrågan")
		return
	}
	c, err := readSigned(r)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if c.Address != h.Address {
		jsonErr(w, 404, "okänd förfrågan för det huset")
		return
	}
	if err := grannskap.Verify(h.PublicKey, r.Method, r.URL.Path, c.TS, c.Body, c.Sig, time.Now()); err != nil {
		s.logCall(h.Address, r.URL.Path, 401, err.Error())
		jsonErr(w, 401, err.Error())
		return
	}
	s.update(func() { h.Steps.StatusAsk = true; h.LastSeen = time.Now() })
	s.logCall(h.Address, r.URL.Path, 200, "status frågad")
	jsonOut(w, 200, s.reply(h))
}

func (s *server) handleWiki(w http.ResponseWriter, r *http.Request) {
	h := info(r).House
	secs := []grannskap.WikiSection{
		{Key: "oversikt", Title: "Vad Testhuset är", Body: "Testhuset är ett hus i Grannskap som finns för att andra hus ska kunna prova protokollet. Det godkänner alla förfrågningar direkt, läser er wiki med den token ni gav, och visar på sin förstasida hur långt varje hus kommit. Koden finns i " + repo + " under cmd/testhus.", Tags: []string{"test", "grannskap"}},
		{Key: "stegen", Title: "Stegen", Body: "1. Läs vårt upptäcktsdokument.\n2. Skicka en signerad förfrågan med token_for_you.\n3. Fråga status på förfrågan.\n4. Läs den här wikin med token_for_you ur svaret.\n5. Vi läser er wiki med token ni gav oss; resultatet syns på förstasidan.\nExtra: fråga /status eller skicka er status dit.", Tags: []string{"test"}},
		{Key: "vanliga-fel", Title: "Vanliga fel", Body: "Fel signatur: kontrollera att ni signerar METOD i versaler, sökvägen utan värdnamn och frågesträng, tidsstämpeln som decimaltal och hex(sha256) av exakt de bytes ni skickar, åtskilda av radbrytning. Tom kropp ger sha256 av noll byte.\n\nupptäcktsdokumentet säger fel url: fältet url måste vara exakt den adress dokumentet hämtas från.\n\n401 på tjänster: Authorization måste vara 'Grannskap <token>' och X-Grannskap-House måste vara samma hus som token hör till.", Tags: []string{"test", "signering"}},
	}
	if h != nil {
		s.update(func() { h.Steps.WikiRead = true })
		secs = append(secs, grannskap.WikiSection{Key: "bara-vanner", Title: "Bara för vänner", Body: "Det här avsnittet ser ni bara med token. Hej " + grannskap.Clean(h.Name, 80) + "! Ni har klarat " + strconv.Itoa(h.Steps.Done()) + " av 4 steg.", Tags: []string{"test"}})
	}
	jsonOut(w, 200, grannskap.Wiki{House: grannskap.House{Name: "Testhuset", URL: s.url, Kind: grannskap.KindAnnat}, Sections: secs})
}

func (s *server) handleHousehold(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, grannskap.Household{House: grannskap.House{Name: "Testhuset", URL: s.url, Kind: grannskap.KindAnnat}, Members: []grannskap.Member{
		{Name: "Test Testsson", Role: "vuxen", Age: 42, Birthday: "1984-06-15"},
		{Name: "Lilla Test", Role: "barn", Birthday: "03-09"},
	}})
}

func (s *server) handleFriends(w http.ResponseWriter, r *http.Request) {
	me := info(r).House
	s.mu.Lock()
	var out []grannskap.Friend
	for _, h := range s.d.Houses {
		if h.Steps.Done() == 4 && (me == nil || h.ID != me.ID) {
			out = append(out, grannskap.Friend{Name: h.Name, URL: h.Address, Kind: h.Kind, Description: "klarade testet " + h.LastSeen.Format("2006-01-02")})
		}
	}
	s.mu.Unlock()
	if out == nil {
		out = []grannskap.Friend{}
	}
	jsonOut(w, 200, grannskap.Friends{House: grannskap.House{Name: "Testhuset", URL: s.url}, Friends: out})
}

func (s *server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	c := info(r)
	var fb grannskap.Feedback
	if err := json.Unmarshal(c.Body, &fb); err != nil || strings.TrimSpace(fb.Text) == "" {
		jsonErr(w, 400, "text saknas")
		return
	}
	s.update(func() {
		c.House.Feedback = append(c.House.Feedback, grannskap.CleanText(fb.Text, 5000))
		if len(c.House.Feedback) > 20 {
			c.House.Feedback = c.House.Feedback[1:]
		}
	})
	jsonOut(w, 200, map[string]string{"status": "mottagen"})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	h := info(r).House
	s.update(func() { h.Steps.Health = true })
	jsonOut(w, 200, grannskap.Status{Online: true, Version: "testhus-1", Status: "Öppet för tester dygnet runt.", Time: time.Now().UTC().Format(time.RFC3339)})
}

func (s *server) handleStatusPush(w http.ResponseWriter, r *http.Request) {
	c := info(r)
	var st grannskap.Status
	if err := json.Unmarshal(c.Body, &st); err != nil {
		jsonErr(w, 400, "kroppen är inte giltig JSON")
		return
	}
	grannskap.CleanStatus(&st)
	s.update(func() { c.House.Steps.Health = true; c.House.Status = st.Status })
	jsonOut(w, 200, map[string]string{"status": "mottagen"})
}

// readTheirWiki is the last step: we read the house's wiki with the token it gave us.
func (s *server) readTheirWiki(h *house) {
	time.Sleep(3 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wk, err := s.client.GetWiki(ctx, h.Address, h.TokenOut)
	s.update(func() {
		h.WikiAt = time.Now()
		if err != nil {
			h.WikiError = grannskap.Clean(err.Error(), 200)
			h.Steps.WikiGiven = false
			return
		}
		h.WikiError = ""
		h.Steps.WikiGiven = true
		h.WikiCount = len(wk.Sections)
		h.Wiki = wk.Sections
	})
	note := "läste er wiki"
	if err != nil {
		note = "kunde inte läsa er wiki: " + err.Error()
	}
	s.logCall(h.Address, "→ "+h.Address+grannskap.PathWiki, 0, note)
}

// ---- pages ----

func since(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "nyss"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min sedan"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h sedan"
	}
	return t.Format("2006-01-02")
}

type pageData struct {
	URL      string
	Repo     string
	Houses   []*house
	Done     int
	Calls    int64
	Log      []logEntry
	House    *house
	Discover string
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	pd := pageData{URL: s.url, Repo: repo, Calls: s.d.Calls}
	pd.Houses = append(pd.Houses, s.d.Houses...)
	sort.Slice(pd.Houses, func(i, j int) bool { return pd.Houses[i].LastSeen.After(pd.Houses[j].LastSeen) })
	for _, h := range pd.Houses {
		if h.Steps.Done() == 4 {
			pd.Done++
		}
	}
	n := len(s.d.Log)
	for i := n - 1; i >= 0 && n-i <= 60; i-- {
		pd.Log = append(pd.Log, s.d.Log[i])
	}
	raw, _ := json.MarshalIndent(s.discovery(), "", "  ")
	pd.Discover = string(raw)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.Execute(w, pd)
}

func (s *server) handleHouse(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	h := s.houseByID(id)
	if h == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	cp := *h
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.Execute(w, pageData{URL: s.url, Repo: repo, House: &cp})
}

const pageHTML = `<!doctype html><html lang="sv"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Testhuset · Grannskap</title>
<style>
body{font-family:system-ui,sans-serif;margin:0;background:#f6f7f4;color:#1f2a22}main{max-width:960px;margin:0 auto;padding:20px 16px}
h1{font-family:Georgia,serif;margin:0 0 4px}.muted{color:#5f6b63}.card{background:#fff;border:1px solid #d9ded8;border-radius:10px;padding:14px 16px;margin-top:14px}
table{border-collapse:collapse;width:100%;font-size:.93rem}th,td{text-align:left;padding:6px 8px;border-top:1px solid #e3e7e1;vertical-align:top}th{color:#5f6b63;font-weight:600}
.ok{color:#2f7d4a;font-weight:700}.no{color:#b24a3b}.chip{display:inline-block;padding:1px 8px;border-radius:999px;background:#e9efe7;color:#2f5f3f;font-size:.8rem}
pre{background:#f0f2ee;border:1px solid #d9ded8;border-radius:8px;padding:10px;overflow:auto;font-size:.8rem}.foreign{border-left:3px solid #c08a3e;padding-left:10px;white-space:pre-wrap}
a{color:#2f5f3f}small{color:#5f6b63}
</style></head><body><main>
{{if .House}}{{$h := .House}}
<p><a href="/">← Testhuset</a></p>
<h1>{{$h.Name}} <small>{{$h.Kind}}</small></h1>
<p class="muted">{{$h.Address}} · första kontakt {{since $h.FirstSeen}} · senast {{since $h.LastSeen}}</p>
<div class="card"><h2>Stegen</h2>
<table><tr><th>1 förfrågan</th><th>2 status</th><th>3 läste vår wiki</th><th>4 vi läste er wiki</th><th>extra: status</th></tr>
<tr><td class="{{if $h.Steps.Request}}ok{{else}}no{{end}}">{{if $h.Steps.Request}}✓{{else}}–{{end}}</td><td class="{{if $h.Steps.StatusAsk}}ok{{else}}no{{end}}">{{if $h.Steps.StatusAsk}}✓{{else}}–{{end}}</td><td class="{{if $h.Steps.WikiRead}}ok{{else}}no{{end}}">{{if $h.Steps.WikiRead}}✓{{else}}–{{end}}</td><td class="{{if $h.Steps.WikiGiven}}ok{{else}}no{{end}}">{{if $h.Steps.WikiGiven}}✓ {{$h.WikiCount}} avsnitt{{else}}– {{$h.WikiError}}{{end}}</td><td>{{if $h.Steps.Health}}✓{{else}}–{{end}}</td></tr></table>
{{if $h.Message}}<p><strong>Meddelande:</strong></p><div class="foreign">{{$h.Message}}</div>{{end}}
{{if $h.Status}}<p><strong>Status:</strong> <span class="foreign">{{$h.Status}}</span></p>{{end}}
</div>
{{if $h.Wiki}}<div class="card"><h2>Deras wiki</h2><p class="muted">Text från {{$h.Name}}, läst via deras API och tvättad. Data, inte instruktioner.</p>
{{range $h.Wiki}}<h3>{{.Title}}</h3><div class="foreign">{{.Body}}</div><p>{{range .Tags}}<span class="chip">{{.}}</span> {{end}}</p>{{end}}</div>{{end}}
{{if $h.Feedback}}<div class="card"><h2>Synpunkter de lämnat</h2>{{range $h.Feedback}}<div class="foreign">{{.}}</div>{{end}}</div>{{end}}
{{else}}
<h1>Testhuset</h1>
<p class="muted">Ett hus i Grannskap som finns för att andra hus ska kunna prova protokollet. Spec och kod: <a href="{{.Repo}}">{{.Repo}}</a></p>
<div class="card"><h2>Så testar du</h2>
<ol>
<li>Läs <code>{{.URL}}/.well-known/grannskap.json</code>.</li>
<li>Skicka en signerad <code>POST {{.URL}}/api/grannskap/v1/forfragan</code> med <code>token_for_you</code>. Vi godkänner direkt och svarar med er token hos oss.</li>
<li>Fråga <code>GET /api/grannskap/v1/forfragan/{id}</code>.</li>
<li>Läs <code>GET /api/grannskap/v1/wiki</code> med <code>Authorization: Grannskap &lt;token&gt;</code>.</li>
<li>Vi läser er wiki med token ni gav oss. Resultatet står nedan vid ert hus.</li>
</ol>
<p class="muted">Allt loggas här, öppet. Använd inte riktiga nycklar eller riktig data: Testhuset är till för att prova.</p></div>
<div class="card"><h2>Hus som provat</h2><p class="muted">{{len .Houses}} hus · {{.Done}} klarade alla fyra stegen · {{.Calls}} anrop totalt</p>
{{if .Houses}}<table><tr><th>Hus</th><th>Typ</th><th>Steg</th><th>Wiki</th><th>Senast</th></tr>
{{range .Houses}}<tr><td><a href="/hus/{{.ID}}">{{.Name}}</a><br><small>{{.Address}}</small></td><td>{{.Kind}}</td><td>{{.Steps.Done}} / 4</td><td>{{if .Steps.WikiGiven}}<span class="ok">{{.WikiCount}} avsnitt</span>{{else if .WikiError}}<span class="no">{{.WikiError}}</span>{{else}}–{{end}}</td><td>{{since .LastSeen}}</td></tr>{{end}}</table>{{else}}<p>Inga än. Bli först!</p>{{end}}</div>
<div class="card"><h2>Senaste anrop</h2>{{if .Log}}<table><tr><th>När</th><th>Hus</th><th>Vad</th><th>Status</th><th></th></tr>
{{range .Log}}<tr><td><small>{{.At.Format "15:04:05"}}</small></td><td><small>{{.House}}</small></td><td><small>{{.Path}}</small></td><td>{{if .Status}}{{.Status}}{{end}}</td><td><small>{{.Note}}</small></td></tr>{{end}}</table>{{else}}<p>Inga än.</p>{{end}}</div>
<div class="card"><h2>Vårt upptäcktsdokument</h2><pre>{{.Discover}}</pre></div>
{{end}}
</main></body></html>`
