package launchd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type call []string

// fakeLaunchctl records calls; fail makes a subcommand fail n times.
func fakeLaunchctl(fail map[string]int, outputs map[string]string) (Runner, *[]call) {
	var calls []call
	return func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append(call{name}, args...))
		if fail[args[0]] > 0 {
			fail[args[0]]--
			return []byte(outputs[args[0]]), errors.New("exit status 5")
		}
		return []byte(outputs[args[0]]), nil
	}, &calls
}

func TestPlistIsValidAndEscaped(t *testing.T) {
	a := Agent{Program: "/Users/a&b/.local/bin/tally", Args: []string{"--port", "/dev/cu.x"},
		Env: map[string]string{"HERDR_SESSION": "<work>"}, Log: "/tmp/tally.log"}
	p := string(a.Plist())
	for _, want := range []string{
		"<string>/Users/a&amp;b/.local/bin/tally</string>\n\t\t<string>serve</string>\n\t\t<string>--port</string>\n\t\t<string>/dev/cu.x</string>",
		"<key>HERDR_SESSION</key>\n\t\t<string>&lt;work&gt;</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>RunAtLoad</key>\n\t<true/>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available")
	}
	f := filepath.Join(t.TempDir(), "a.plist")
	os.WriteFile(f, []byte(p), 0o644)
	if out, err := exec.Command("plutil", "-lint", f).CombinedOutput(); err != nil {
		t.Errorf("plutil: %v: %s", err, out)
	}
}

func TestPlistWithoutEnv(t *testing.T) {
	if strings.Contains(string(Agent{Program: "/t"}.Plist()), "EnvironmentVariables") {
		t.Error("empty env rendered")
	}
}

func TestInstallWritesAndReloads(t *testing.T) {
	home := t.TempDir()
	run, calls := fakeLaunchctl(map[string]int{"bootstrap": 2}, nil) // launchd still letting go
	plist, err := Install(Agent{Program: "/bin/tally"}, home, run)
	if err != nil {
		t.Fatal(err)
	}
	want, log := Paths(home)
	if plist != want {
		t.Errorf("plist = %s", plist)
	}
	b, err := os.ReadFile(plist)
	if err != nil || !strings.Contains(string(b), "<string>"+log+"</string>") {
		t.Errorf("plist %v lacks the default log path:\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Dir(log)); err != nil {
		t.Errorf("log dir: %v", err)
	}
	var verbs []string
	for _, c := range *calls {
		verbs = append(verbs, c[1])
	}
	if got := strings.Join(verbs, " "); got != "bootout enable bootstrap bootstrap bootstrap kickstart" {
		t.Errorf("launchctl calls: %s", got)
	}
	if boot := (*calls)[4]; boot[3] != plist || !strings.HasPrefix(boot[2], "gui/") {
		t.Errorf("bootstrap call: %v", boot)
	}
	if kick := (*calls)[5]; !strings.HasSuffix(kick[2], "/"+Label) {
		t.Errorf("kickstart call: %v", kick)
	}
}

func TestInstallReportsBootstrapFailure(t *testing.T) {
	run, _ := fakeLaunchctl(map[string]int{"bootstrap": 99}, map[string]string{"bootstrap": "Bootstrap failed: 5: Input/output error"})
	if _, err := Install(Agent{Program: "/bin/tally"}, t.TempDir(), run); err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstall(t *testing.T) {
	home := t.TempDir()
	run, calls := fakeLaunchctl(nil, nil)
	plist, _ := Install(Agent{Program: "/bin/tally"}, home, run)
	*calls = nil
	if _, err := Uninstall(home, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("plist still there: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0][1] != "bootout" {
		t.Errorf("calls = %v", *calls)
	}
	if _, err := Uninstall(home, run); err != nil {
		t.Errorf("second uninstall: %v", err)
	}
}

// Captured from launchctl print on macOS, trimmed.
const printed = "gui/501/local.tally = {\n" +
	"\tactive count = 1\n" +
	"\tpath = /Users/x/Library/LaunchAgents/local.tally.plist\n" +
	"\tstate = running\n" +
	"\n" +
	"\tprogram = /Users/x/.local/bin/tally\n" +
	"\targuments = {\n" +
	"\t\t/Users/x/.local/bin/tally\n" +
	"\t\tserve\n" +
	"\t}\n" +
	"\tenvironment = {\n" +
	"\t\tstate = not-this-one\n" +
	"\t}\n" +
	"\tpid = 4242\n" +
	"\tlast exit code = (never exited)\n" +
	"}\n"

func TestParsePrint(t *testing.T) {
	want := State{Loaded: true, State: "running", PID: 4242, LastExit: "(never exited)", Program: "/Users/x/.local/bin/tally"}
	if got := ParsePrint([]byte(printed)); got != want {
		t.Errorf("got %+v", got)
	}
}

func TestStatusNotLoaded(t *testing.T) {
	run, _ := fakeLaunchctl(map[string]int{"print": 1}, map[string]string{
		"print": "Bad request.\nCould not find service \"local.tally\" in domain for user gui: 501\n"})
	st, err := Status(run)
	if err != nil || st.Loaded {
		t.Errorf("st = %+v, err = %v", st, err)
	}
}

func TestParsePrintPended(t *testing.T) {
	out := "gui/501/local.tally = {\n\tstate = not running\n\tpended nondemand spawn = inefficient\n\tlast exit code = (never exited)\n}\n"
	if st := ParsePrint([]byte(out)); st.State != "not running" || st.Pended != "inefficient" || st.PID != 0 {
		t.Errorf("got %+v", st)
	}
}
