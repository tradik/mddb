package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newSelfUpdateCmd builds `mddb-cli self-update` (OPS-019).
func newSelfUpdateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update mddb-cli to the latest release",
		Long: `Checks for a newer release and, unless --check is given, installs it.

The download is verified against the release's checksums.txt before anything on
disk is touched, and the current binary is kept as <path>.bak.

A binary installed through snap or running inside a container is not replaced —
the packaging channel owns it, and the command says which one to use instead.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSelfUpdate(cmd, checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Only report whether an update is available")
	return cmd
}

func runSelfUpdate(cmd *cobra.Command, checkOnly bool) error {
	out := cmd.OutOrStdout()
	current := CurrentVersion()

	release, err := FetchLatestRelease(context.Background())
	if err != nil {
		return err
	}

	if IsDevelopmentBuild() {
		_, _ = fmt.Fprintf(out, "This is a development build; the latest release is %s.\n", release.Version)
		_, _ = fmt.Fprintln(out, "Self-update only applies to released binaries.")
		return nil
	}

	if CompareVersions(release.Version, current) <= 0 {
		_, _ = fmt.Fprintf(out, "mddb-cli %s is up to date.\n", current)
		return nil
	}

	_, _ = fmt.Fprintf(out, "A newer release is available: %s (you have %s)\n", release.Version, current)
	if release.URL != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", release.URL)
	}

	// Refusing comes after reporting on purpose: someone on a snap still wants
	// to know an update exists, they just need a different way to get it.
	if method := DetectInstallMethod(); method != InstallBinary {
		_, _ = fmt.Fprintf(out, "\nmddb-cli is %s\n", UpdateInstructions(method))
		return nil
	}

	if checkOnly {
		_, _ = fmt.Fprintln(out, "\nRun `mddb-cli self-update` to install it.")
		return nil
	}

	return installUpdate(cmd, current, release.Version)
}

func installUpdate(cmd *cobra.Command, current, version string) error {
	out := cmd.OutOrStdout()

	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate this binary: %w", err)
	}
	// Through a symlink, replace what the link points at rather than the link.
	if resolved, err := resolveExecutable(path); err == nil {
		path = resolved
	}

	name := artifactName("mddb-cli", version)
	_, _ = fmt.Fprintf(out, "\nDownloading %s...\n", name)

	tarball, err := DownloadAndVerify(context.Background(), version, name)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "Checksum verified.")

	binary, err := ExtractBinary(tarball, "mddb-cli")
	if err != nil {
		return err
	}

	backup, err := ReplaceBinary(path, binary)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Updated %s → %s\n", current, version)
	_, _ = fmt.Fprintf(out, "  installed: %s\n", path)
	_, _ = fmt.Fprintf(out, "  previous:  %s\n", backup)
	return nil
}

// resolveExecutable follows symlinks to the real binary.
var resolveExecutable = func(path string) (string, error) {
	return realpath(path)
}
