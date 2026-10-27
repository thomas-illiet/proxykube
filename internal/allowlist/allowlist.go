package allowlist

import (
	"fmt"
	"net"
	"strings"

	"github.com/thomasilliet/proxykube/internal/config"
	"golang.org/x/net/idna"
)

type rule struct {
	domain   string
	wildcard bool
	ports    map[int]struct{}
}

type Allowlist struct{ rules []rule }

// New validates and compiles configuration rules into an immutable allowlist.
func New(rules []config.Rule) (*Allowlist, error) {
	list := &Allowlist{rules: make([]rule, 0, len(rules))}
	for _, item := range rules {
		raw := strings.TrimSpace(item.Domain)
		wildcard := strings.HasPrefix(raw, "*.")
		if wildcard {
			raw = strings.TrimPrefix(raw, "*.")
		}
		domain, err := normalize(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed domain %q: %w", item.Domain, err)
		}
		if wildcard && net.ParseIP(domain) != nil {
			return nil, fmt.Errorf("IP address %q cannot be a wildcard", item.Domain)
		}
		ports := make(map[int]struct{}, len(item.Ports))
		for _, port := range item.Ports {
			ports[port] = struct{}{}
		}
		list.rules = append(list.rules, rule{domain: domain, wildcard: wildcard, ports: ports})
	}
	return list, nil
}

// Allowed reports whether host and port match at least one configured rule.
func (a *Allowlist) Allowed(host string, port int) bool {
	host, err := normalize(host)
	if err != nil {
		return false
	}
	for _, item := range a.rules {
		if _, ok := item.ports[port]; !ok {
			continue
		}
		if (!item.wildcard && host == item.domain) || (item.wildcard && strings.HasSuffix(host, "."+item.domain)) {
			return true
		}
	}
	return false
}

// normalize canonicalizes an IP address or internationalized DNS name.
func normalize(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || strings.ContainsAny(value, "/:@") {
		return "", fmt.Errorf("malformed host")
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	value, err := idna.Lookup.ToASCII(value)
	if err != nil || value == "" || !strings.Contains(value, ".") {
		return "", fmt.Errorf("malformed DNS name")
	}
	return value, nil
}
