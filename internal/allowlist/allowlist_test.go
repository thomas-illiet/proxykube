package allowlist

import (
	"testing"

	"github.com/thomasilliet/proxykube/internal/config"
)

// TestAllowed covers exact, wildcard, port, case, and literal-IP matching.
func TestAllowed(t *testing.T) {
	list, err := New([]config.Rule{
		{Domain: "Example.COM.", Ports: []int{80, 443}},
		{Domain: "*.example.org", Ports: []int{443}},
		{Domain: "10.0.0.1", Ports: []int{8080}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host string
		port int
		want bool
	}{
		{"example.com", 80, true},
		{"EXAMPLE.com.", 443, true},
		{"example.com", 22, false},
		{"api.example.org", 443, true},
		{"deep.api.example.org", 443, true},
		{"example.org", 443, false},
		{"badexample.org", 443, false},
		{"10.0.0.1", 8080, true},
		{"10.0.0.2", 8080, false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := list.Allowed(tt.host, tt.port); got != tt.want {
				t.Fatalf("Allowed(%q, %d) = %v, want %v", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

// TestInvalidWildcardIP verifies that IP addresses cannot be wildcard rules.
func TestInvalidWildcardIP(t *testing.T) {
	_, err := New([]config.Rule{{Domain: "*.10.0.0.1", Ports: []int{443}}})
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestInvalidAndInternationalizedDomains covers invalid and IDNA domain normalization.
func TestInvalidAndInternationalizedDomains(t *testing.T) {
	for _, domain := range []string{"", "not-a-domain", "bad/name.example", "*.10.0.0.1"} {
		t.Run(domain, func(t *testing.T) {
			if _, err := New([]config.Rule{{Domain: domain, Ports: []int{443}}}); err == nil {
				t.Fatal("expected invalid domain error")
			}
		})
	}
	list, err := New([]config.Rule{{Domain: "bücher.example", Ports: []int{443}}})
	if err != nil {
		t.Fatal(err)
	}
	if !list.Allowed("xn--bcher-kva.example", 443) {
		t.Fatal("expected IDNA name to match")
	}
	if list.Allowed("bad/name.example", 443) {
		t.Fatal("malformed request host must be denied")
	}
}
