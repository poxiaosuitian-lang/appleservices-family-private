package icloud

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Persisted sessions contain per-origin snapshots: cookiejar.Cookies does not
// expose the original Domain. Remove those host-only copies when Apple rotates
// the corresponding shared cookie, otherwise requests send old and new tokens.
type webSessionJar struct {
	http.CookieJar
	mu       sync.Mutex
	restored []hmeCookie
}

func (j *webSessionJar) restore(u *url.URL, item hmeCookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.CookieJar.SetCookies(u, []*http.Cookie{{Name: item.Name, Value: item.Value, Path: item.Path, Secure: item.Secure, Expires: item.Expires}})
	j.restored = append(j.restored, item)
}

func (j *webSessionJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		domain := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		// Only an actual response from the owning iCloud domain may replace
		// restored copies. Do not infer a broader scope from a cookie name.
		if (domain != "icloud.com" && domain != "icloud.com.cn") || !cookieDomainMatches(u.Hostname(), domain) {
			continue
		}
		path := cookie.Path
		if path == "" || path[0] != '/' {
			path = u.Path[:strings.LastIndex(u.Path, "/")+1]
			path = strings.TrimSuffix(path, "/")
			if path == "" {
				path = "/"
			}
		}
		kept := j.restored[:0]
		for _, item := range j.restored {
			scope, err := url.Parse(item.Scope)
			itemPath := item.Path
			if itemPath == "" {
				itemPath = "/"
			}
			if err == nil && item.Name == cookie.Name && itemPath == path && cookieDomainMatches(scope.Hostname(), domain) {
				j.CookieJar.SetCookies(scope, []*http.Cookie{{Name: item.Name, Path: itemPath, MaxAge: -1}})
			} else {
				kept = append(kept, item)
			}
		}
		j.restored = kept
	}
	j.CookieJar.SetCookies(u, cookies)
}

func cookieDomainMatches(host, domain string) bool {
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}
