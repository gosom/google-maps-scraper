package webshare

import "time"

// WhatsMyIPResponse represents the response from the whatsmyip endpoint
type WhatsMyIPResponse struct {
	IPAddress string `json:"ip_address"`
}

// IPAuthorization represents an IP authorization entry
type IPAuthorization struct {
	ID         int        `json:"id"`
	IPAddress  string     `json:"ip_address"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// IPAuthorizationListResponse represents the paginated list of IP authorizations
type IPAuthorizationListResponse struct {
	Count    int               `json:"count"`
	Next     *string           `json:"next"`
	Previous *string           `json:"previous"`
	Results  []IPAuthorization `json:"results"`
}

// Proxy represents a single proxy from the Webshare API
type Proxy struct {
	ID               string    `json:"id"`
	Username         string    `json:"username"`
	Password         string    `json:"password"`
	ProxyAddress     string    `json:"proxy_address"`
	Port             int       `json:"port"`
	Valid            bool      `json:"valid"`
	LastVerification time.Time `json:"last_verification"`
	CountryCode      string    `json:"country_code"`
	CityName         string    `json:"city_name"`
	CreatedAt        time.Time `json:"created_at"`
}

// ProxyListResponse represents the paginated proxy list response
type ProxyListResponse struct {
	Count    int     `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []Proxy `json:"results"`
}
