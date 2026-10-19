package systemd

import (
	"os"
	"strings"
	"testing"
)

func TestUnitFile(t *testing.T) {
	a := Agent{Program: "/home/me/.local/bin/tally", Args: []string{"--remote", "--no-device"},
		Env: map[string]string{"HERDR_SESSION": "work", "HERDR_SOCKET_PATH": "/run/a b.sock"}}
	want := `[Unit]
Description=tally: herdr agent status for the agent pads

[Service]
ExecStart=/home/me/.local/bin/tally serve --remote --no-device
Environment=HERDR_SESSION=work
Environment="HERDR_SOCKET_PATH=/run/a b.sock"
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`
	if got := string(a.File()); got != want {
		t.Errorf("unit\n%s\nwant\n%s", got, want)
	}
}

func TestInstallWritesTheUnitAndStartsIt(t *testing.T) {
	home := t.TempDir()
	var ran []string
	run := func(name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	path, err := Install(Agent{Program: "/bin/tally"}, home, run)
	if err != nil {
		t.Fatal(err)
	}
	if path != home+"/.config/systemd/user/tally.service" {
		t.Errorf("path = %s", path)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "ExecStart=/bin/tally serve\n") {
		t.Errorf("unit = %s", b)
	}
	want := "systemctl --user daemon-reload|systemctl --user enable tally.service|systemctl --user restart tally.service"
	if got := strings.Join(ran, "|"); got != want {
		t.Errorf("ran %s", got)
	}

	ran = nil
	if _, err := Uninstall(home, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("unit file left behind")
	}
	if !strings.HasPrefix(strings.Join(ran, "|"), "systemctl --user disable --now tally.service") {
		t.Errorf("ran %v", ran)
	}
}

func TestParseShow(t *testing.T) {
	out := `LoadState=loaded
ActiveState=active
SubState=running
MainPID=4242
Result=success
ExecStart={ path=/home/me/.local/bin/tally ; argv[]=/home/me/.local/bin/tally serve --remote ; ignore_errors=no ; start_time=[n/a] }
`
	st := ParseShow([]byte(out))
	if !st.Loaded || st.Active != "active" || st.Sub != "running" || st.PID != 4242 || st.Exec != "/home/me/.local/bin/tally serve --remote" {
		t.Errorf("state = %+v", st)
	}
	if st := ParseShow([]byte("LoadState=not-found\nActiveState=inactive\n")); st.Loaded {
		t.Errorf("not-found parsed as loaded: %+v", st)
	}
}
