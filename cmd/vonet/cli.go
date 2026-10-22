package main

import (
	"fmt"
	"io"

	"vonet/internal/buildinfo"
)

func runVersion() {
	fmt.Println("vonet " + buildinfo.Build())
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `vonet %s

Usage:
  vonet              No arguments: interactive management menu when run as
                     root on a TTY, otherwise the server. A managed service
                     (systemd or OpenWrt procd) starts the server unchanged.
  vonet serve        Run the server in the foreground (use from a TTY when
                     vonet without arguments would enter the menu).
  vonet version      Print the build version and exit.
  vonet doctor       Diagnose USB modem, AT, QMI, PC/SC and proxy UDP paths.
                     Use --repair-dji-qmi on Linux to restore the factory-ID
                     DJI/Baiwang 2ca3:4006 AT/QMI interface bindings and wake
                     QMI without changing NV.
  vonet carrier import-ipcc [flags] FILE.ipcc
                     Convert an Apple carrier bundle into a reviewable VoNet
                     profile. Preview is the default; --install writes it to
                     carrier-profiles.d and takes effect after restart.
                     Flags: --bundle NAME --id ID --document-only --install
                            --profile-dir DIR.
  vonet update       Check GitHub for a newer release and self-update.
                     Flags:
                       --check           Only report whether an update is available.
                       --repo owner/name GitHub repository (default: $VONET_REPO or MengMengCode/VoNet).
                       --target path     Binary to replace (default: running exe).
                       --force           Reinstall even at the same version.
                     Environment:
                       VONET_REPO        Fallback for --repo.
                       GITHUB_TOKEN      Optional bearer token for private repos
                                         or higher rate limits.
  vonet menu         Interactive lifecycle menu (root on the host):
                       toggle language, change password, change the Web port,
                       restart, update, uninstall.
  vonet help         Show this help message.

When run without a subcommand on a non-TTY (e.g. systemd), vonet starts the
HTTP server using VONET_* environment variables or $VONET_CONFIG for
configuration.
`, buildinfo.Version)
}
