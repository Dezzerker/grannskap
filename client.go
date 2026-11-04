package grannskap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client calls other houses on behalf of this one. Identity is this house's address and private
// key; a friend's token is passed per call.
type Client struct {
	Address string // this house, e.g. https://hem.hexance.se
	PrivKey string // base64 ed25519
	HTTP    *http.Client
}

// NewClient builds a client with sane timeouts.
func NewClient(address, privKey string) *Client {
	return &Client{Address: address, PrivKey: privKey, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

const maxBody = 2 << 20

// FetchDiscovery reads a house's discovery document and checks the basics.
func (c *Client) FetchDiscovery(ctx context.Context, address string) (*Discovery, error) {
	addr, err := NormalizeAddress(address)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+PathDiscovery, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Grannskap/1 (+"+c.Address+")")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kunde inte nå %s: %w", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s svarade %d på upptäcktsdokumentet", addr, resp.StatusCode)
	}
	var d Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&d); err != nil {
		return nil, fmt.Errorf("upptäcktsdokumentet på %s går inte att läsa: %w", addr, err)
	}
	if d.Grannskap == "" || d.PublicKey == "" || d.Name == "" {
		return nil, errors.New("upptäcktsdokumentet saknar grannskap-version, namn eller publik nyckel")
	}
	if !ValidKind(d.Kind) {
		d.Kind = KindAnnat
	}
	if d.Kind == "" {
		d.Kind = KindHushall
	}
	if d.Parent != "" {
		if p, err := NormalizeAddress(d.Parent); err == nil {
			d.Parent = p
		} else {
			d.Parent = ""
		}
	}
	if u, err := NormalizeAddress(d.URL); err != nil || u != addr {
		return nil, fmt.Errorf("upptäcktsdokumentet säger %q men hämtades från %s", d.URL, addr)
	}
	d.URL = addr
	CleanDiscovery(&d)
	return &d, nil
}

// call sends a signed request to a house. token "" means an unauthenticated (but signed) call.
func (c *Client) call(ctx context.Context, address, method, path, token string, body any, out any) (int, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, address+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	ts := time.Now().Unix()
	sig, err := Sign(c.PrivKey, method, path, ts, raw)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Grannskap/1 (+"+c.Address+")")
	req.Header.Set(HeaderHouse, c.Address)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(HeaderSignature, sig)
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Grannskap "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("kunde inte nå %s: %w", address, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var e Error
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return resp.StatusCode, fmt.Errorf("%s svarade %d: %s", address, resp.StatusCode, Clean(e.Error, 200))
		}
		return resp.StatusCode, fmt.Errorf("%s svarade %d", address, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("svaret från %s går inte att läsa: %w", address, err)
		}
	}
	return resp.StatusCode, nil
}

// SendRequest asks a house for friendship.
func (c *Client) SendRequest(ctx context.Context, address string, req Request) (*RequestReply, error) {
	var out RequestReply
	if _, err := c.call(ctx, address, http.MethodPost, PathRequest, "", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestStatus polls a request we sent.
func (c *Client) RequestStatus(ctx context.Context, address string, id int64) (*RequestReply, error) {
	var out RequestReply
	if _, err := c.call(ctx, address, http.MethodGet, PathRequest+"/"+strconv.FormatInt(id, 10), "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWiki reads a house's wiki (public sections without a token, all shared ones with).
func (c *Client) GetWiki(ctx context.Context, address, token string) (*Wiki, error) {
	var out Wiki
	if _, err := c.call(ctx, address, http.MethodGet, PathWiki, token, nil, &out); err != nil {
		return nil, err
	}
	CleanWiki(&out)
	return &out, nil
}

// GetHousehold reads the members a friend shares with us.
func (c *Client) GetHousehold(ctx context.Context, address, token string) (*Household, error) {
	var out Household
	if _, err := c.call(ctx, address, http.MethodGet, PathHousehold, token, nil, &out); err != nil {
		return nil, err
	}
	CleanHousehold(&out)
	return &out, nil
}

// GetFriends reads a friend's friends (the ones they chose to show).
func (c *Client) GetFriends(ctx context.Context, address, token string) (*Friends, error) {
	var out Friends
	if _, err := c.call(ctx, address, http.MethodGet, PathFriends, token, nil, &out); err != nil {
		return nil, err
	}
	CleanFriends(&out)
	return &out, nil
}

// SendFeedback leaves a comment in a friend's feedback box.
func (c *Client) SendFeedback(ctx context.Context, address, token, text string) error {
	_, err := c.call(ctx, address, http.MethodPost, PathFeedback, token, Feedback{Text: strings.TrimSpace(text)}, nil)
	return err
}

// GetStatus asks a friend how it is doing. A 429 comes back as *TooSoonError.
func (c *Client) GetStatus(ctx context.Context, address, token string) (*Status, error) {
	var out Status
	code, err := c.call(ctx, address, http.MethodGet, PathStatus, token, nil, &out)
	if code == http.StatusTooManyRequests {
		return nil, &TooSoonError{Message: err.Error()}
	}
	if err != nil {
		return nil, err
	}
	CleanStatus(&out)
	return &out, nil
}

// PushStatus sends our status to a friend (their choice whether to keep it).
func (c *Client) PushStatus(ctx context.Context, address, token string, st Status) error {
	_, err := c.call(ctx, address, http.MethodPost, PathStatus, token, st, nil)
	return err
}

// TooSoonError means the friend does not want to be asked this often.
type TooSoonError struct{ Message string }

func (e *TooSoonError) Error() string { return e.Message }
