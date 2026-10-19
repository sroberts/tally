package pairing

import (
	"reflect"
	"testing"
)

func testEndpoints() []Endpoint {
	return Endpoints("4b19148852ca3c36", "laptop.tail1.ts.net", []string{"192.168.0.140", "10.0.0.5"}, Port)
}

func TestOfferedFollowsMenuOrder(t *testing.T) {
	if got, want := Offered(testEndpoints()), []string{"tailscale", "bonjour", "lan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Offered = %v, want %v", got, want)
	}
	noTS := Endpoints("4b19148852ca3c36", "", []string{"192.168.0.140"}, Port)
	if got, want := Offered(noTS), []string{"bonjour", "lan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Offered without tailscale = %v, want %v", got, want)
	}
}

func TestSelectTailscaleOnly(t *testing.T) {
	got, err := Select(testEndpoints(), []string{"tailscale"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{{Kind: "tailscale", Host: "laptop.tail1.ts.net", Port: Port}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select = %+v, want %+v", got, want)
	}
}

func TestSelectKeepsOriginalOrder(t *testing.T) {
	got, err := Select(testEndpoints(), []string{"lan", "bonjour"})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range got {
		kinds = append(kinds, e.Kind)
	}
	if want := []string{"bonjour", "host", "host"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
}

func TestSelectMissingPathFails(t *testing.T) {
	noTS := Endpoints("4b19148852ca3c36", "", []string{"192.168.0.140"}, Port)
	if _, err := Select(noTS, []string{"tailscale"}); err == nil {
		t.Fatal("Select tailscale without a tailscale endpoint succeeded")
	}
}

func TestParseVia(t *testing.T) {
	got, err := ParseVia(" Tailscale, bonjour ,")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tailscale", "bonjour"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseVia = %v, want %v", got, want)
	}
	for _, bad := range []string{"", "wifi", "tailscale,usb"} {
		if _, err := ParseVia(bad); err == nil {
			t.Errorf("ParseVia(%q) succeeded", bad)
		}
	}
}

func TestParseChoice(t *testing.T) {
	offered := []string{"tailscale", "bonjour", "lan"}
	cases := map[string][]string{
		"":     offered,
		"1":    {"tailscale"},
		"1,3":  {"tailscale", "lan"},
		" 2 3": {"bonjour", "lan"},
	}
	for in, want := range cases {
		got, err := ParseChoice(in, offered)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseChoice(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "4", "tailscale"} {
		if _, err := ParseChoice(bad, offered); err == nil {
			t.Errorf("ParseChoice(%q) succeeded", bad)
		}
	}
}
