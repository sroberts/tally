package pairing

import (
	"fmt"
	"strconv"
	"strings"
)

// Paths are the ways tally pair offers to reach this host, in menu order.
// "lan" covers every host endpoint: raw addresses that change with the
// network, unlike the Tailscale name or the Bonjour instance.
var Paths = []string{"tailscale", "bonjour", "lan"}

// PathHints says when each path works, for tally pair's menu.
var PathHints = map[string]string{
	"tailscale": "any network; the phone needs tailscale on",
	"bonjour":   "same wi-fi, or the laptop on the phone's hotspot",
	"lan":       "this network only; changes when you move",
}

// PathOf is the path an endpoint belongs to.
func PathOf(e Endpoint) string {
	if e.Kind == "host" {
		return "lan"
	}
	return e.Kind
}

// Offered lists the paths that have at least one endpoint, in menu order.
func Offered(eps []Endpoint) []string {
	var out []string
	for _, p := range Paths {
		for _, e := range eps {
			if PathOf(e) == p {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// ParseVia reads a --via list such as "tailscale,bonjour".
func ParseVia(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if PathHints[f] == "" {
			return nil, fmt.Errorf("unknown path %q: choose from %s", f, strings.Join(Paths, ", "))
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no path given: choose from %s", strings.Join(Paths, ", "))
	}
	return out, nil
}

// ParseChoice reads a menu answer: numbers into offered ("1" or "1,3"), or
// an empty answer for all of them.
func ParseChoice(answer string, offered []string) ([]string, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return offered, nil
	}
	var out []string
	for _, f := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(offered) {
			return nil, fmt.Errorf("%q is not one of 1-%d", f, len(offered))
		}
		out = append(out, offered[n-1])
	}
	return out, nil
}

// Select keeps the endpoints on the chosen paths, in their original order.
// A chosen path with no endpoint is an error, so asking for Tailscale when
// it isn't running fails instead of pairing without it.
func Select(eps []Endpoint, paths []string) ([]Endpoint, error) {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	have := map[string]bool{}
	var out []Endpoint
	for _, e := range eps {
		if want[PathOf(e)] {
			out = append(out, e)
			have[PathOf(e)] = true
		}
	}
	for _, p := range paths {
		if !have[p] {
			switch p {
			case "tailscale":
				return nil, fmt.Errorf("no tailscale name: is tailscale running with MagicDNS on?")
			case "lan":
				return nil, fmt.Errorf("no LAN address: is this machine on a network?")
			default:
				return nil, fmt.Errorf("no %s endpoint", p)
			}
		}
	}
	return out, nil
}
