package icloud

import (
	"errors"
	"net/url"
	"strings"
)

// ExportServerCookies is used only by the explicit server-sync action after List
// verifies the session. Never return these credentials to the dashboard.
func (c *HideMyEmailClient) ExportServerCookies() (map[string]string, string, error) {
	u, err := url.Parse(c.session.ServiceURL)
	if err != nil || u.Scheme != "https" || c.session.AppleID != c.appleID || c.session.DSID == "" {
		return nil, "", errors.New("网页会话绑定无效")
	}
	host := "icloud.com"
	if strings.HasSuffix(u.Hostname(), ".icloud.com.cn") {
		host = "icloud.com.cn"
	} else if !strings.HasSuffix(u.Hostname(), ".icloud.com") {
		return nil, "", errors.New("网页服务地址无效")
	}
	cookies := map[string]string{}
	for _, origin := range []string{"https://setup." + host + "/setup/ws/1", c.session.ServiceURL} {
		parsed, _ := url.Parse(origin)
		for _, cookie := range c.jar.Cookies(parsed) {
			if strings.HasPrefix(strings.ToUpper(cookie.Name), "X-APPLE-") && cookie.Value != "" {
				cookies[cookie.Name] = cookie.Value
			}
		}
	}
	if cookies["X-APPLE-WEBAUTH-TOKEN"] == "" || cookies["X-APPLE-WEBAUTH-USER"] == "" {
		return nil, "", errors.New("网页会话缺少必要凭据")
	}
	return cookies, host, nil
}
