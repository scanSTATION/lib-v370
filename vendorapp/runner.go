package vendorapp

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Mode selects how the vendor app binary is invoked.
type Mode int

const (
	ModeFlatpak Mode = iota // default: flatpak run
	ModeNative              // native: direct binary
)

// FlatpakID is the Flatpak application ID for the vendor app.
const FlatpakID = "net.epson.epsonscan2"

// Config holds execution parameters.
type Config struct {
	Mode Mode // execution mode

	// NativeBinaryPath is the path to the vendor app binary for ModeNative.
	// Ignored in ModeFlatpak.
	NativeBinaryPath string

	// NoNetwork removes network access from the Flatpak sandbox by passing
	// --unshare=network to flatpak run. The V370 is a USB scanner, so network
	// access is unnecessary. Enabled by default for security; set to false to
	// allow network scanner discovery (Avahi). Ignored in ModeNative.
	NoNetwork bool
}

// DefaultConfig returns a Flatpak-based configuration with network isolation
// enabled.
func DefaultConfig() Config {
	return Config{Mode: ModeFlatpak, NoNetwork: true}
}

// Device represents a scanner discovered by the vendor app --list.
type Device struct {
	Name    string // human-readable device name
	ModelID string // internal model ID (e.g. ES00DD)
}

// ListDevices runs `epsonscan2 --list` and parses the output.
func ListDevices(cfg Config) ([]Device, error) {
	out, err := runVendorApp(cfg, "--list")
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w\noutput: %s", err, out)
	}
	return parseDeviceList(out), nil
}

// parseDeviceList extracts device entries from `epsonscan2 --list` output.
//
// Expected format:
//
//	=== List of available devices ==
//	device ID :Perfection V37/V370/GT-S640/F740
//	ModelID:ES00DD
func parseDeviceList(output string) []Device {
	var devices []Device
	lines := strings.Split(output, "\n")
	var current Device

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "device ID :") {
			current = Device{Name: strings.TrimPrefix(line, "device ID :")}
		} else if strings.HasPrefix(line, "ModelID:") {
			current.ModelID = strings.TrimPrefix(line, "ModelID:")
			if current.Name != "" {
				devices = append(devices, current)
			}
			current = Device{}
		}
	}
	return devices
}

// ScanOptions controls a single scan invocation.
type ScanOptions struct {
	// SettingsFile is the path to the .SF2 file to use.
	SettingsFile string
	// DeviceName is the scanner device name (optional; auto-detected if empty).
	DeviceName string
	// OutputDir is the directory where scan output should be saved.
	// For Flatpak mode, this must be accessible inside the sandbox
	// (use ~/Documents or ~/Pictures, or pass a host path with --filesystem).
	OutputDir string
}

// flatpakFilesystemArgs returns extra flatpak run arguments to grant
// access to the output directory and settings file location.
func flatpakFilesystemArgs(opts ScanOptions, settingsFile string) []string {
	var args []string
	if opts.OutputDir != "" {
		args = append(args, "--filesystem="+opts.OutputDir)
	}
	// Ensure the directory containing the settings file is accessible.
	if settingsFile != "" {
		dir := filepath.Dir(settingsFile)
		args = append(args, "--filesystem="+dir)
	}
	return args
}

// Scan triggers a scan using the vendor app CLI.
// It returns the combined stdout/stderr output.
func Scan(cfg Config, opts ScanOptions) (string, error) {
	args := []string{"--scan"}

	if opts.DeviceName != "" {
		args = append(args, opts.DeviceName)
	} else {
		// Auto-detect the first available device.
		devices, err := ListDevices(cfg)
		if err != nil {
			return "", fmt.Errorf("auto-detecting device: %w", err)
		}
		if len(devices) == 0 {
			return "", fmt.Errorf("no scanner devices found")
		}
		args = append(args, devices[0].Name)
	}

	args = append(args, opts.SettingsFile)

	extraFlatpakArgs := flatpakFilesystemArgs(opts, opts.SettingsFile)
	out, err := runVendorAppWithExtra(cfg, extraFlatpakArgs, args...)
	if err != nil {
		return out, fmt.Errorf("scan failed: %w\noutput: %s", err, out)
	}
	return out, nil
}

// Status is the parsed result of `epsonscan2 --get-status`.
type Status struct {
	Ready   bool   // scanner reachable, no caution reported
	Message string // "ready", or the caution text the scanner reported
}

// ErrScanner is wrapped by GetStatus when the vendor app reports a scanner error.
var ErrScanner = errors.New("scanner error")

// GetStatus queries the scanner device status.
//
// The vendor app --get-status prints nothing and exits 0 when the scanner is
// fine (verified on 6.7.82.0). Errors are printed as "ERROR : ..." with exit
// code 255 — or exit code 0 for some errors (e.g. invalid options) — so the
// output has to be interpreted, not just the exit code.
func GetStatus(cfg Config) (Status, error) {
	out, err := runVendorApp(cfg, "--get-status")
	return parseStatus(out, err)
}

// parseStatus interprets the vendor app --get-status output.
func parseStatus(out string, runErr error) (Status, error) {
	var lines, errs []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "libusb:") {
			continue // libusb debug noise
		}
		if strings.HasPrefix(l, "ERROR") {
			errs = append(errs, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "ERROR"), " :")))
			continue
		}
		lines = append(lines, l)
	}
	if len(errs) > 0 {
		return Status{}, fmt.Errorf("%w: %s", ErrScanner, strings.Join(errs, "; "))
	}
	if runErr != nil {
		return Status{}, fmt.Errorf("%w: %v (output: %s)", ErrScanner, runErr, strings.TrimSpace(out))
	}
	if len(lines) == 0 {
		return Status{Ready: true, Message: "ready"}, nil
	}
	return Status{Ready: false, Message: strings.Join(lines, "; ")}, nil
}

// runVendorApp invokes the vendor app binary with the given arguments.
func runVendorApp(cfg Config, args ...string) (string, error) {
	return runVendorAppWithExtra(cfg, nil, args...)
}

// buildFlatpakArgs constructs the argument vector for `flatpak run`.
// It inserts --unshare=network before any extra args when cfg.NoNetwork is set.
func buildFlatpakArgs(cfg Config, extraFlatpakArgs []string, args []string) []string {
	flatpakArgs := []string{"run", "--command=epsonscan2"}
	if cfg.NoNetwork {
		flatpakArgs = append(flatpakArgs, "--unshare=network")
	}
	flatpakArgs = append(flatpakArgs, extraFlatpakArgs...)
	flatpakArgs = append(flatpakArgs, FlatpakID)
	flatpakArgs = append(flatpakArgs, args...)
	return flatpakArgs
}

// runVendorAppWithExtra invokes the vendor app with extra Flatpak run arguments
// (e.g. --filesystem overrides). The extra args are ignored in native mode.
func runVendorAppWithExtra(cfg Config, extraFlatpakArgs []string, args ...string) (string, error) {
	var cmd *exec.Cmd

	switch cfg.Mode {
	case ModeFlatpak:
		cmd = exec.Command("flatpak", buildFlatpakArgs(cfg, extraFlatpakArgs, args)...)

	case ModeNative:
		if cfg.NativeBinaryPath == "" {
			return "", fmt.Errorf("native binary path not set")
		}
		cmd = exec.Command(cfg.NativeBinaryPath, args...)

	default:
		return "", fmt.Errorf("unknown execution mode %d", cfg.Mode)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	combined := stdout.String() + stderr.String()
	return combined, err
}

// WriteSettingsFile writes the .SF2 content to a temp file and returns its path.
// The file is placed in the user's Documents directory so that the Flatpak
// sandbox can access it (Flatpak has xdg-documents access).
// The caller should remove the file when done.
func WriteSettingsFile(content []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	dir := filepath.Join(home, "Documents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating documents dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "vendorapp-*.SF2")
	if err != nil {
		return "", fmt.Errorf("creating temp settings file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return "", fmt.Errorf("writing settings file: %w", err)
	}
	return f.Name(), nil
}

// DesktopDir returns the user's Desktop directory path.
// The vendor app's Folder=2 saves scanned files here.
func DesktopDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, "Desktop"), nil
}

// FindScannedFile searches outputDir for the most recently created scan output.
// The vendor app CLI saves files with the FileNamePrefix from the settings.
func FindScannedFile(outputDir, prefix string) (string, error) {
	// Try common image extensions first.
	var matches []string
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".tif", ".tiff", ".pdf"} {
		p := filepath.Join(outputDir, prefix+"*"+ext)
		m, _ := filepath.Glob(p)
		matches = append(matches, m...)
	}
	if len(matches) == 0 {
		// Fall back to any file with the prefix.
		pattern := filepath.Join(outputDir, prefix+"*")
		fallback, err := filepath.Glob(pattern)
		if err != nil {
			return "", fmt.Errorf("globbing for scan output: %w", err)
		}
		matches = fallback
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no scan output found in %s with prefix %q", outputDir, prefix)
	}

	// Return the most recently modified file.
	newest := matches[0]
	newestInfo, err := os.Stat(newest)
	if err != nil {
		return "", fmt.Errorf("statting %s: %w", newest, err)
	}
	for _, m := range matches[1:] {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if info.ModTime().After(newestInfo.ModTime()) {
			newest = m
			newestInfo = info
		}
	}
	return newest, nil
}

// MoveScannedFile moves the scanned file from src to the target directory,
// optionally renaming it. If targetPath is empty, the file is moved to
// targetDir with its original filename. If targetPath is set, the file is
// moved and renamed to targetPath.
func MoveScannedFile(src, targetDir, targetPath string) (string, error) {
	if targetDir == "" && targetPath == "" {
		return src, nil // no move needed
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("creating target dir %s: %w", targetDir, err)
	}

	var dest string
	if targetPath != "" {
		dest = targetPath
	} else {
		dest = filepath.Join(targetDir, filepath.Base(src))
	}

	// If source and destination are the same, skip.
	if src == dest {
		return dest, nil
	}

	// Remove destination if it exists (overwrite).
	os.Remove(dest)

	if err := os.Rename(src, dest); err != nil {
		// Rename can fail across filesystems; fall back to copy + remove.
		if err := copyFile(src, dest); err != nil {
			return "", fmt.Errorf("moving scan output: %w", err)
		}
		os.Remove(src)
	}
	return dest, nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}
