package scrapemate

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Proxy is a struct for proxy
type Proxy struct {
	URL      string
	Username string
	Password string
}

func (o *Proxy) FullURL() string {
	if o.Username != "" && o.Password != "" {
		pu, err := url.Parse(o.URL)
		if err != nil {
			return o.URL
		}

		pu.User = url.UserPassword(o.Username, o.Password)

		return pu.String()
	}

	return o.URL
}

func NewProxy(u string) (Proxy, error) {
	if !strings.Contains(u, "://") {
		u = "socks5://" + u
	}

	pu, err := url.Parse(u)
	if err != nil {
		return Proxy{}, err
	}

	supportedSchemes := []string{"socks5", "http", "https", "socks5h"}
	scheme := strings.ToLower(pu.Scheme)
	valid := slices.Contains(supportedSchemes, scheme)

	if !valid {
		return Proxy{}, fmt.Errorf("invalid proxy type: %s", scheme)
	}

	var username, password string
	if pu.User != nil {
		username = pu.User.Username()
		password, _ = pu.User.Password()
	}

	cleanURL := fmt.Sprintf("%s://%s", scheme, pu.Host)

	return Proxy{
		URL:      cleanURL,
		Username: username,
		Password: password,
	}, nil
}
