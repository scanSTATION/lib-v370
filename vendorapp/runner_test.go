package vendorapp

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestBuildFlatpakArgsNoNetworkDefault(t *testing.T) {
	cfg := DefaultConfig() // NoNetwork should be true
	if !cfg.NoNetwork {
		t.Fatal("DefaultConfig should have NoNetwork=true")
	}

	args := buildFlatpakArgs(cfg, nil, []string{"--list"})

	if !slices.Contains(args, "--unshare=network") {
		t.Errorf("expected --unshare=network in flatpak args, got %v", args)
	}
}

func TestBuildFlatpakArgsNoNetworkTrue(t *testing.T) {
	cfg := Config{Mode: ModeFlatpak, NoNetwork: true}

	args := buildFlatpakArgs(cfg, []string{"--filesystem=/tmp"}, []string{"--scan", "device", "file.SF2"})

	if !slices.Contains(args, "--unshare=network") {
		t.Errorf("expected --unshare=network in flatpak args, got %v", args)
	}
	// --unshare=network must come before extra args and the app ID
	networkIdx := slices.Index(args, "--unshare=network")
	extraIdx := slices.Index(args, "--filesystem=/tmp")
	if networkIdx > extraIdx {
		t.Errorf("--unshare=network (idx %d) should come before --filesystem (idx %d)", networkIdx, extraIdx)
	}
}

func TestBuildFlatpakArgsAllowNetwork(t *testing.T) {
	cfg := Config{Mode: ModeFlatpak, NoNetwork: false}

	args := buildFlatpakArgs(cfg, nil, []string{"--list"})

	if slices.Contains(args, "--unshare=network") {
		t.Errorf("did not expect --unshare=network when NoNetwork=false, got %v", args)
	}
}

func TestBuildFlatpakArgsStructure(t *testing.T) {
	cfg := Config{Mode: ModeFlatpak, NoNetwork: true}
	extra := []string{"--filesystem=/home/user/Documents"}
	scanArgs := []string{"--scan", "Perfection V37/V370/GT-S640/F740", "test.SF2"}

	args := buildFlatpakArgs(cfg, extra, scanArgs)

	// Verify the overall structure:
	// run --command=epsonscan2 --unshare=network --filesystem=... <app-id> --scan device file
	want := []string{
		"run", "--command=epsonscan2", "--unshare=network",
		"--filesystem=/home/user/Documents",
		FlatpakID,
		"--scan", "Perfection V37/V370/GT-S640/F740", "test.SF2",
	}
	if len(args) != len(want) {
		t.Fatalf("args length = %d, want %d; got %v", len(args), len(want), args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("args[%d] = %q, want %q (full: %v)", i, args[i], w, args)
		}
	}
}

// Outputs observed from the vendor app 6.7.82.0 on 2026-11-07.
func TestParseStatus(t *testing.T) {
	exit255 := errors.New("exit status 255")
	cases := []struct {
		name      string
		out       string
		runErr    error
		wantErr   bool
		wantReady bool
		wantMsg   string
	}{
		{"silent success", "", nil, false, true, "ready"},
		{"whitespace only", "\n  \n", nil, false, true, "ready"},
		{"device unreachable", "libusb: warning [op_get_configuration] device unconfigured\n" +
			"libusb: warning [op_get_configuration] device unconfigured\n" +
			"ERROR : An unexpected error occurred. The vendor app will close.\n", exit255, true, false, ""},
		{"error with exit 0", "ERROR : Option format invalid.\n", nil, true, false, ""},
		{"exit code only", "", exit255, true, false, ""},
		{"caution text", "Cover open\n", nil, false, false, "Cover open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := parseStatus(c.out, c.runErr)
			if c.wantErr {
				if !errors.Is(err, ErrScanner) {
					t.Fatalf("err = %v, want ErrScanner", err)
				}
				return
			}
			if err != nil || st.Ready != c.wantReady || st.Message != c.wantMsg {
				t.Fatalf("got %+v, %v", st, err)
			}
		})
	}
	if _, err := parseStatus("ERROR : Option format invalid.", nil); err == nil || !strings.Contains(err.Error(), "Option format invalid") {
		t.Errorf("error message not propagated: %v", err)
	}
}
