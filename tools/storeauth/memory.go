package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

var (
	errMemoryState = errors.New("invalid in-memory state")
	errKeyNotFound = errors.New("key not found")
	errCookies     = errors.New("invalid cookie state")
)

type memoryKeys struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryKeys() *memoryKeys { return &memoryKeys{data: make(map[string][]byte)} }

func (k *memoryKeys) Get(key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	data, ok := k.data[key]
	if !ok {
		return nil, errKeyNotFound
	}
	return append([]byte(nil), data...), nil
}

func (k *memoryKeys) Set(key string, data []byte) error {
	if (key != anisetteKey && key != "account") || len(data) > maxStateBytes ||
		(key == anisetteKey && !validState(data)) {
		return errMemoryState
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.data[key])
	k.data[key] = append([]byte(nil), data...)
	return nil
}

func (k *memoryKeys) Remove(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.data[key])
	delete(k.data, key)
	return nil
}

func (k *memoryKeys) clear() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for key, data := range k.data {
		clear(data)
		delete(k.data, key)
	}
}

type trackingJar struct {
	jar          *cookiejar.Jar
	mu           sync.Mutex
	events       []cookieEvent
	count, bytes int
	err          error
}

func newTrackingJar() (*trackingJar, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	return &trackingJar{jar: jar}, nil
}

func (j *trackingJar) Cookies(origin *url.URL) []*http.Cookie { return j.jar.Cookies(origin) }
func (j *trackingJar) Save() error                            { return j.failure() }

func (j *trackingJar) SetCookies(origin *url.URL, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != nil {
		return
	}
	if !validCookieOrigin(origin) || j.count+len(cookies) > maxCookies {
		j.err = errCookies
		return
	}
	event := cookieEvent{URL: (&url.URL{Scheme: "https", Host: origin.Host, Path: origin.Path}).String()}
	received := time.Now()
	for _, cookie := range cookies {
		if !validCookie(cookie) {
			j.err = errCookies
			return
		}
		// Apple can send deletion cookies for a sibling Store domain. A
		// standard jar ignores these; they must not abort an otherwise valid
		// login or be replayed into a different origin by the parent.
		if !cookieAppliesToOrigin(origin, cookie) {
			continue
		}
		copy := *cookie
		copy.Unparsed = append([]string(nil), cookie.Unparsed...)
		// Replaying a relative MaxAge later would extend a short-lived cookie.
		// Keep deletion markers, but export positive lifetimes as absolute dates.
		if copy.MaxAge > 0 {
			duration := time.Duration(copy.MaxAge) * time.Second
			if duration/time.Second != time.Duration(copy.MaxAge) {
				j.err = errCookies
				return
			}
			copy.Expires = received.Add(duration)
			copy.MaxAge = 0
		}
		event.Cookies = append(event.Cookies, &copy)
	}
	encoded, err := json.Marshal(event)
	if err != nil || j.bytes+len(encoded)+1 > maxWireBytes {
		j.err = errCookies
		return
	}
	j.count += len(cookies)
	j.bytes += len(encoded) + 1
	j.jar.SetCookies(origin, cookies)
	// Bag and GSA provisioning cookies stay in this process; only Store-origin
	// cookie updates are exported to the parent's commerce session.
	if storeCookieHost(origin.Hostname()) && len(event.Cookies) > 0 {
		j.events = append(j.events, event)
	}
}

func (j *trackingJar) failure() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

func (j *trackingJar) snapshot() []cookieEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]cookieEvent, len(j.events))
	for i, event := range j.events {
		result[i].URL = event.URL
		for _, cookie := range event.Cookies {
			copy := *cookie
			copy.Unparsed = append([]string(nil), cookie.Unparsed...)
			result[i].Cookies = append(result[i].Cookies, &copy)
		}
	}
	return result
}

func storeCookieHost(host string) bool {
	host = strings.ToLower(host)
	if host == "auth.itunes.apple.com" || host == "buy.itunes.apple.com" {
		return true
	}
	const suffix = "-buy.itunes.apple.com"
	return strings.HasPrefix(host, "p") && strings.HasSuffix(host, suffix) &&
		podPattern.MatchString(strings.TrimSuffix(strings.TrimPrefix(host, "p"), suffix))
}

func validCookieOrigin(origin *url.URL) bool {
	return origin != nil && origin.Scheme == "https" && origin.User == nil &&
		origin.Opaque == "" && origin.RawPath == "" && origin.Fragment == "" && (origin.Port() == "" || origin.Port() == "443") &&
		len(origin.String()) <= 8192 && (storeCookieHost(origin.Hostname()) || provisioningCookieHost(origin.Hostname()))
}

func provisioningCookieHost(host string) bool {
	switch strings.ToLower(host) {
	case "init.itunes.apple.com", "gsa.apple.com", "gsa-ssl.apple.com", "gsas.apple.com":
		return true
	default:
		return false
	}
}

func validCookie(cookie *http.Cookie) bool {
	return cookie != nil && cookie.Valid() == nil && len(cookie.Name)+len(cookie.Value) <= 16384
}

func cookieAppliesToOrigin(origin *url.URL, cookie *http.Cookie) bool {
	if cookie.Domain == "" {
		return true
	}
	domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
	host := strings.ToLower(origin.Hostname())
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	return domain == "apple.com" || strings.HasSuffix(domain, ".apple.com")
}
