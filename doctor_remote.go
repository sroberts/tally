package main

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/sroberts/tally/internal/actions"
	"github.com/sroberts/tally/internal/doctor"
	"github.com/sroberts/tally/internal/herdr"
	"github.com/sroberts/tally/internal/pairing"
	"github.com/sroberts/tally/internal/remote"
	"github.com/sroberts/tally/internal/web"
)

// checkRemote checks the remote listener's side: the listener, paired phones,
// Bonjour, Tailscale, the firewall and the write path.
func checkRemote() []doctor.Result {
	store, err := pairing.DefaultStore()
	if err != nil {
		return []doctor.Result{{Name: "remote", Level: doctor.Warn, Detail: err.Error()}}
	}
	addrs := pairing.LANAddrs()
	_, fp, certErr := store.LoadCertificate()
	var probeErr error
	if certErr == nil {
		probeErr = probe(net.JoinHostPort("127.0.0.1", strconv.Itoa(pairing.Port)), fp)
	}
	listening := certErr == nil && probeErr == nil
	results := []doctor.Result{doctor.Listener(certErr, probeErr, pairing.Port, addrs)}

	st, err := store.Load()
	if err != nil {
		results = append(results, doctor.Result{Name: "phones", Level: doctor.Fail, Detail: err.Error()})
	} else {
		var labels []string
		for _, p := range st.Phones {
			labels = append(labels, p.Label)
		}
		results = append(results, doctor.Phones(labels))
	}

	if listening && st.HostID != "" {
		results = append(results, checkBonjour(pairing.BonjourInstance(st.HostID)))
	} else {
		results = append(results, doctor.Result{Name: "bonjour", Level: doctor.Info, Detail: "not checked: the listener is not running"})
	}

	out, err := web.TailscaleStatus(context.Background(), web.FindTailscale())
	results = append(results, doctor.Tailscale(out, err))
	results = append(results, checkFirewall(listening, fp, addrs))
	return append(results, checkWritePath())
}

func probe(addr, fp string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return remote.Probe(ctx, addr, fp)
}

// checkBonjour browses for tally's own _tally._tcp instance.
func checkBonjour(instance string) doctor.Result {
	r := doctor.Result{Name: "bonjour"}
	res, err := zeroconf.NewResolver(nil)
	if err != nil {
		r.Level, r.Detail = doctor.Warn, "can't browse: "+err.Error()
		return r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	found := make(chan *zeroconf.ServiceEntry, 4)
	if err := res.Lookup(ctx, instance, remote.Service, "local.", found); err != nil {
		r.Level, r.Detail = doctor.Warn, err.Error()
		return r
	}
	select {
	case e := <-found:
		r.Level, r.Detail = doctor.OK, fmt.Sprintf("%s.%s.local resolves to port %d", instance, remote.Service, e.Port)
	case <-ctx.Done():
		r.Level, r.Detail = doctor.Warn, instance+" did not resolve within 3 s; phones on this network will fall back to other paths"
		if runtime.GOOS == "linux" {
			r.Fix = "avahi-browse -rt " + remote.Service + " (if avahi is installed)"
		}
	}
	return r
}

// checkFirewall reads the OS firewall and, when the listener runs, tries
// the port on the first LAN address as a phone on the same Wi-Fi would.
func checkFirewall(listening bool, fp string, addrs []string) doctor.Result {
	var r doctor.Result
	if runtime.GOOS == "linux" {
		ufw, _ := runCmd("ufw", "status")
		fwd, _ := runCmd("firewall-cmd", "--state")
		r = doctor.LinuxFirewall(string(ufw), string(fwd), pairing.Port)
	} else {
		const fw = "/usr/libexec/ApplicationFirewall/socketfilterfw"
		program, _ := executable()
		global, _ := runCmd(fw, "--getglobalstate")
		app, _ := runCmd(fw, "--getappblocked", program)
		r = doctor.MacFirewall(string(global), string(app), program)
	}
	if listening && len(addrs) > 0 {
		addr := net.JoinHostPort(addrs[0], strconv.Itoa(pairing.Port))
		if err := probe(addr, fp); err != nil {
			r.Level = max(r.Level, doctor.Warn)
			r.Detail += "; " + addr + " is not reachable: " + err.Error()
		} else {
			r.Detail += "; reachable at " + addr
		}
	}
	return r
}

// checkWritePath dry-runs ENTER on the focused agent through the action
// table: the target check and key encoding run, and nothing is written.
func checkWritePath() doctor.Result {
	r := doctor.Result{Name: "write path"}
	path, err := herdr.SocketPath()
	if err != nil {
		r.Level, r.Detail = doctor.Warn, err.Error()
		return r
	}
	reader := herdr.New(path)
	snap, err := reader.Snapshot()
	if err != nil {
		r.Level, r.Detail = doctor.Info, "not checked: herdr is not reachable"
		return r
	}
	if len(snap.Agents) == 0 {
		r.Level, r.Detail = doctor.Info, "not checked: no agents"
		return r
	}
	target := snap.Agents[0].PaneID
	rec := &actions.Recorder{}
	t := &actions.Table{W: rec, Snapshot: func() (*herdr.Snapshot, error) { return snap, nil }}
	if err := t.Do(actions.Request{Name: "submit", Target: target}); err != nil {
		r.Level, r.Detail = doctor.Fail, "dry run failed: "+err.Error()
		return r
	}
	r.Level, r.Detail = doctor.OK, "dry run of submit on "+target+": "+rec.Calls[0]
	return r
}
