package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"vonet/internal/auth"
	"vonet/internal/buildinfo"
	"vonet/internal/config"
	"vonet/internal/developer"
	"vonet/internal/device"
	"vonet/internal/exportproxy"
	"vonet/internal/extensions"
	"vonet/internal/httpsmode"
	"vonet/internal/loghub"
	"vonet/internal/mcp"
	"vonet/internal/modem"
	"vonet/internal/nodeadapter"
	"vonet/internal/pcsc"
	"vonet/internal/remoteproc"
	"vonet/internal/server"
	"vonet/internal/store"
	"vonet/internal/update"
	"vonet/internal/vowifi"
	"vonet/internal/vowifi/ike"
	"vonet/internal/vowifi/ims"
	"vonet/internal/vowifi/integration"
	vowifiruntime "vonet/internal/vowifi/runtime"
	"vonet/internal/vowifisettings"
	"vonet/web"
)

const (
	flightModeTransitionTimeout = 45 * time.Second
	deviceStartupTimeout        = 4 * time.Minute
	stableModemWaitTimeout      = 45 * time.Second
	ec20RegistrationGrace       = 2 * time.Minute
	ec20RegistrationEscalation  = 3 * time.Minute
)

type registrationRecoveryAction uint8

const (
	registrationRecoveryNone registrationRecoveryAction = iota
	registrationRecoveryReregister
	registrationRecoveryReboot
)

type registrationRecoveryEpisode struct {
	iccid       string
	searchingAt time.Time
	lastAttempt time.Time
	attempts    int
	pending     registrationRecoveryAction
}

type registrationRecoveryTracker struct {
	mu         sync.Mutex
	grace      time.Duration
	escalation time.Duration
	episodes   map[string]registrationRecoveryEpisode
}

type registrationRecoveryDevices interface {
	Refresh(context.Context, string) (device.Snapshot, error)
	PrepareRegistration(context.Context, string, string, string) error
	ReRegisterOperator(context.Context, string) (device.OperatorSelection, error)
	Reboot(context.Context, string) error
}

type registrationRecoveryPolicies interface {
	CardPolicy(context.Context, string) (store.CardPolicy, error)
}

func explicitRegistrationRecoveryFlightMode(snapshot device.Snapshot) bool {
	return snapshot.ModeKnown && snapshot.OperatingMode == 4
}

func newRegistrationRecoveryTracker(grace, escalation time.Duration) *registrationRecoveryTracker {
	return &registrationRecoveryTracker{
		grace: grace, escalation: escalation,
		episodes: make(map[string]registrationRecoveryEpisode),
	}
}

func (tracker *registrationRecoveryTracker) observe(
	deviceID string,
	snapshot *device.Snapshot,
	now time.Time,
) registrationRecoveryAction {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if snapshot == nil {
		return registrationRecoveryNone
	}
	if explicitRegistrationRecoveryFlightMode(*snapshot) ||
		snapshot.RegistrationStatus == 1 || snapshot.RegistrationStatus == 5 {
		delete(tracker.episodes, deviceID)
		return registrationRecoveryNone
	}
	iccid := strings.TrimSpace(snapshot.ICCID)
	episode, found := tracker.episodes[deviceID]
	if found && iccid != "" && episode.iccid != iccid {
		delete(tracker.episodes, deviceID)
		found = false
	}
	// A CFUN reboot temporarily clears the snapshot while the SIM and ICCID are
	// still initializing. Preserve an existing episode through that interval so
	// the one-reboot limit cannot be re-armed by the reboot itself.
	if !snapshot.SIMReady ||
		!strings.HasPrefix(strings.ToUpper(strings.TrimSpace(snapshot.Model)), "EC20") ||
		iccid == "" ||
		(snapshot.RegistrationStatus != 0 && snapshot.RegistrationStatus != 2) {
		return registrationRecoveryNone
	}
	if !found {
		tracker.episodes[deviceID] = registrationRecoveryEpisode{iccid: iccid, searchingAt: now}
		return registrationRecoveryNone
	}
	if episode.pending != registrationRecoveryNone {
		return registrationRecoveryNone
	}
	if episode.attempts == 0 && now.Sub(episode.searchingAt) >= tracker.grace {
		episode.pending = registrationRecoveryReregister
		tracker.episodes[deviceID] = episode
		return registrationRecoveryReregister
	}
	if episode.attempts == 1 && now.Sub(episode.lastAttempt) >= tracker.escalation {
		episode.pending = registrationRecoveryReboot
		tracker.episodes[deviceID] = episode
		return registrationRecoveryReboot
	}
	return registrationRecoveryNone
}

func (tracker *registrationRecoveryTracker) complete(
	deviceID string,
	expectedICCID string,
	action registrationRecoveryAction,
	issued bool,
	now time.Time,
) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	episode, found := tracker.episodes[deviceID]
	if !found || episode.iccid != strings.TrimSpace(expectedICCID) || episode.pending != action {
		return
	}
	episode.pending = registrationRecoveryNone
	if issued {
		episode.attempts++
		episode.lastAttempt = now
	}
	tracker.episodes[deviceID] = episode
}

func registrationRecoveryEligible(snapshot device.Snapshot, expectedICCID string) bool {
	iccid := strings.TrimSpace(snapshot.ICCID)
	return snapshot.SIMReady && !snapshot.FlightMode && !snapshot.RadioOff &&
		strings.HasPrefix(strings.ToUpper(strings.TrimSpace(snapshot.Model)), "EC20") &&
		iccid != "" && iccid == strings.TrimSpace(expectedICCID) &&
		(snapshot.RegistrationStatus == 0 || snapshot.RegistrationStatus == 2)
}

func registrationRecoverySuperseded(snapshot device.Snapshot, expectedICCID string) bool {
	iccid := strings.TrimSpace(snapshot.ICCID)
	model := strings.ToUpper(strings.TrimSpace(snapshot.Model))
	if explicitRegistrationRecoveryFlightMode(snapshot) || (model != "" && !strings.HasPrefix(model, "EC20")) {
		return true
	}
	if iccid != "" && iccid != strings.TrimSpace(expectedICCID) {
		return true
	}
	if snapshot.RegistrationStatus == 1 || snapshot.RegistrationStatus == 5 {
		return true
	}
	return snapshot.SIMReady && iccid != "" &&
		snapshot.RegistrationStatus != 0 && snapshot.RegistrationStatus != 2
}

func revalidateRegistrationRecovery(
	ctx context.Context,
	devices registrationRecoveryDevices,
	deviceID string,
	expectedICCID string,
) (device.Snapshot, bool, error) {
	snapshot, err := devices.Refresh(ctx, deviceID)
	if err != nil {
		return device.Snapshot{}, false, err
	}
	return snapshot, registrationRecoveryEligible(snapshot, expectedICCID), nil
}

func runRegistrationRecovery(
	ctx context.Context,
	logger *slog.Logger,
	policies registrationRecoveryPolicies,
	devices registrationRecoveryDevices,
	deviceID string,
	expectedICCID string,
	action registrationRecoveryAction,
) bool {
	issued := false
	current, eligible, err := revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID)
	if err != nil || !eligible {
		return false
	}
	if action == registrationRecoveryReboot {
		// Revalidate immediately before the destructive operation so a recovered
		// modem, SIM swap, or flight-mode transition cannot inherit a stale action.
		if _, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID); err != nil || !eligible {
			return false
		}
		if err = devices.Reboot(ctx, deviceID); err != nil {
			logger.Warn("EC20 registration recovery reboot failed", "device_id", deviceID, "error", err)
			return true
		}
		// CFUN reboot resets EC20 CID 1 on affected firmware. Wait for the AT
		// transport to return, then reload the current card policy and revalidate
		// before every modem operation.
		for attempt := 0; attempt < 12 && ctx.Err() == nil; attempt++ {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return true
			case <-timer.C:
			}
			current, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID)
			if err != nil {
				continue
			}
			if !eligible {
				if registrationRecoverySuperseded(current, expectedICCID) {
					return true
				}
				continue
			}
			policy, policyErr := policies.CardPolicy(ctx, strings.TrimSpace(current.ICCID))
			if policyErr == nil && strings.TrimSpace(policy.APN) != "" {
				current, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID)
				if err != nil {
					continue
				}
				if !eligible {
					if registrationRecoverySuperseded(current, expectedICCID) {
						return true
					}
					continue
				}
				ipVersion := policy.IPVersion
				if ipVersion == "" {
					ipVersion = "IPV4V6"
				}
				if err = devices.PrepareRegistration(ctx, deviceID, policy.APN, ipVersion); err != nil {
					continue
				}
			}
			current, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID)
			if err != nil {
				continue
			}
			if !eligible {
				if registrationRecoverySuperseded(current, expectedICCID) {
					return true
				}
				continue
			}
			if _, err = devices.ReRegisterOperator(ctx, deviceID); err == nil {
				logger.Info("EC20 registration recovery rebooted modem", "device_id", deviceID)
				return true
			}
		}
		logger.Warn("EC20 registration recovery could not resume after reboot", "device_id", deviceID)
		return true
	}

	policy, policyErr := policies.CardPolicy(ctx, strings.TrimSpace(current.ICCID))
	if policyErr == nil && strings.TrimSpace(policy.APN) != "" {
		if _, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID); err != nil || !eligible {
			return issued
		}
		ipVersion := policy.IPVersion
		if ipVersion == "" {
			ipVersion = "IPV4V6"
		}
		issued = true
		if err = devices.PrepareRegistration(ctx, deviceID, policy.APN, ipVersion); err != nil {
			logger.Warn("EC20 registration recovery could not prepare PDP context", "device_id", deviceID, "error", err)
		}
	}
	if _, eligible, err = revalidateRegistrationRecovery(ctx, devices, deviceID, expectedICCID); err != nil || !eligible {
		return issued
	}
	issued = true
	if _, err = devices.ReRegisterOperator(ctx, deviceID); err != nil {
		logger.Warn("EC20 registration recovery failed", "device_id", deviceID, "error", err)
		return true
	}
	logger.Info("EC20 registration recovery requested", "device_id", deviceID)
	return issued
}

func deviceStartupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), deviceStartupTimeout)
}

func startupFlightModeContext(parent context.Context) (context.Context, context.CancelFunc) {
	// Bound each EC20 CFUN transition inside the larger device-startup budget.
	return context.WithTimeout(parent, flightModeTransitionTimeout)
}

func main() {
	logs := loghub.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}), 2000)
	logger := slog.New(logs)

	args := os.Args[1:]
	switch subcommand, rest := splitSubcommand(args); subcommand {
	case "":
		// No subcommand: TTY+root → interactive menu (operator on the host);
		// otherwise run the server. systemd runs vonet with stdin=/dev/null
		// (non-TTY) so the unit keeps starting the server unchanged. Non-root
		// on a TTY also falls through to the server rather than erroring on
		// runMenu's root requirement.
		if term.IsTerminal(int(os.Stdin.Fd())) && os.Geteuid() == 0 {
			if err := runMenu(logger); err != nil {
				logger.Error("menu failed", "error", err)
				os.Exit(1)
			}
		} else {
			if err := run(logger, logs); err != nil {
				logger.Error("server stopped", "error", err)
				os.Exit(1)
			}
		}
	case "serve":
		// Explicit foreground server. Use this when vonet with no arguments
		// would otherwise enter the menu (root on a TTY) but a server is wanted.
		if err := run(logger, logs); err != nil {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		runVersion()
	case "token":
		if err := runToken(rest, os.Stdout); err != nil {
			logger.Error("token command failed", "error", err)
			os.Exit(1)
		}
	case "auth-cleanup":
		if err := runAuthCleanup(rest, os.Stdout); err != nil {
			logger.Error("auth cleanup failed", "error", err)
			os.Exit(1)
		}
	case "update":
		if err := update.Run(logger, rest); err != nil {
			logger.Error("update failed", "error", err)
			os.Exit(1)
		}
	case "doctor":
		if err := runDoctor(rest); err != nil {
			logger.Error("doctor failed", "error", err)
			os.Exit(1)
		}
	case "carrier":
		if err := runCarrier(rest, os.Stdout); err != nil {
			logger.Error("carrier command failed", "error", err)
			os.Exit(1)
		}
	case "menu":
		if err := runMenu(logger); err != nil {
			logger.Error("menu failed", "error", err)
			os.Exit(1)
		}
	case "develop":
		// Hidden subcommand: intentionally not listed in printUsage or the
		// interactive menu. It toggles the developer-mode flag that gates the
		// entire plugin/extension system; the flag takes effect on next start.
		if err := runDevelop(rest, logger); err != nil {
			logger.Error("develop failed", "error", err)
			os.Exit(2)
		}
	case "bootstrap-admin":
		// Installer-only command. The password is read from stdin so it never
		// appears in argv, an environment file, or process listings.
		if err := runBootstrapAdmin(rest); err != nil {
			logger.Error("bootstrap admin failed", "error", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "vonet: unknown subcommand %q\n\n", subcommand)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

// splitSubcommand returns the first non-flag token as the subcommand and the
// remaining args. An empty arg list yields ("", nil) → server mode.
func splitSubcommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func run(logger *slog.Logger, logs *loghub.Hub) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	carrierProfileDir := filepath.Join(filepath.Dir(cfg.DatabasePath), "carrier-profiles.d")
	if err := vowifi.LoadCarrierProfileDirectory(carrierProfileDir); err != nil {
		return fmt.Errorf("load installed carrier profiles: %w", err)
	}
	instanceLock, err := lockServerInstance(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer instanceLock.Close()
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()

	database, err := store.Open(startupContext, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	developerEnabled := isDeveloperEnabled(startupContext, database)
	pluginRoot := filepath.Join(filepath.Dir(cfg.DatabasePath), "plugins")
	legacyExportProxyConfig := filepath.Join(pluginRoot, exportproxy.ReservedID, "data", "configs.json")
	if !developerEnabled {
		if err := developer.ResetExperimental(startupContext, database); err != nil {
			return fmt.Errorf("reset disabled developer settings: %w", err)
		}
		if err := exportproxy.RemoveLegacyConfig(legacyExportProxyConfig); err != nil {
			return fmt.Errorf("remove legacy export proxy configuration: %w", err)
		}
	}
	httpsManager, err := httpsmode.New(
		startupContext,
		database,
		filepath.Join(filepath.Dir(cfg.DatabasePath), "tls"),
		cfg.Address,
	)
	if err != nil {
		return fmt.Errorf("configure self-signed HTTPS: %w", err)
	}
	if cfg.SecureCookies && !httpsManager.Enabled() {
		logger.Warn(
			"VONET_SECURE_COOKIES=true forces Secure cookies; plain HTTP browsers cannot authenticate unless a trusted TLS proxy provides HTTPS",
			"category", "security",
		)
	}

	// The plugin/extension system is gated behind a hidden developer-mode flag.
	// When off (the default) the manager is never created and the server receives
	// a nil Extensions handle, so every /extensions* and /plugin-assets/* route
	// returns 503/404 and the SPA hides the plugin surface.
	var extensionManager *extensions.Manager
	var exportProxyManager *exportproxy.Manager
	if developerEnabled {
		exportProxyManager, err = exportproxy.New(startupContext, database, logger, legacyExportProxyConfig)
		if err != nil {
			return fmt.Errorf("create built-in export proxy: %w", err)
		}
		defer exportProxyManager.Close()
		extensionManager, err = extensions.NewManager(
			pluginRoot,
			logger,
		)
		if err != nil {
			return fmt.Errorf("create plugin manager: %w", err)
		}
		defer extensionManager.Close()
	} else {
		logger.Info("developer mode is off; plugin system disabled")
	}

	authService, err := auth.New(database, auth.Options{
		SessionTTL: cfg.SessionTTL,
	})
	if err != nil {
		return err
	}
	if _, adminErr := database.CurrentAdmin(startupContext); adminErr != nil {
		if errors.Is(adminErr, store.ErrNotFound) {
			return errors.New("administrator is not initialized; run vonet bootstrap-admin before starting the service")
		}
		return fmt.Errorf("read administrator: %w", adminErr)
	}

	cardReaders := pcsc.New()
	deviceLogger := logger.With("category", "hardware")
	deviceManager, err := device.NewManager(device.Options{
		Discoverer:  modem.NewSystemDiscovererWithOptions(modem.DiscoveryOptions{ProbeUnknownUSB: cfg.ProbeUnknownUSB}),
		CardReaders: cardReaders,
		Logger:      deviceLogger,
		MBNProfileForICCID: func(ctx context.Context, iccid string) (string, error) {
			policy, err := database.CardPolicy(ctx, iccid)
			if errors.Is(err, store.ErrNotFound) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			return policy.MBNProfile, nil
		},
	})
	if err != nil {
		return fmt.Errorf("create device manager: %w", err)
	}
	// Device startup has a separate budget because EC20 CFUN transitions can
	// temporarily remove the USB serial transport, and OpenStick 410 deliberately
	// delays its persisted VoWiFi policy after a cold boot. Do not reuse the short
	// database/configuration deadline for this hardware lifecycle.
	deviceStartupContext, cancelDeviceStartup := deviceStartupContext(startupContext)
	defer cancelDeviceStartup()
	stableModemContext, cancelStableModem := context.WithTimeout(deviceStartupContext, stableModemWaitTimeout)
	if err := deviceManager.WaitForStableModem(
		stableModemContext,
		20*time.Second,
		time.Second,
		20*time.Second,
	); err != nil {
		logger.Warn("wait for stable modem enumeration", "error", err)
	}
	cancelStableModem()
	if err := deviceManager.Start(deviceStartupContext); err != nil {
		logger.Warn("device discovery is not available at startup", "error", err)
	}
	if err := provisionDiscoveredDevices(deviceStartupContext, database, deviceManager); err != nil {
		logger.Warn("automatic first-run device provisioning failed", "error", err)
	}
	configureDeviceBackends(deviceStartupContext, logger, database, deviceManager)
	restoreDefaultCellularRadios(deviceStartupContext, logger, database, deviceManager)
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deviceManager.Stop(stopContext); err != nil {
			logger.Warn("stop device manager", "error", err)
		}
	}()
	pollContext, cancelPolling := context.WithCancel(context.Background())
	defer cancelPolling()

	var onIncomingCall func(context.Context, ims.ReceivedCall) error

	nodeAdapterDBPath := filepath.Join(filepath.Dir(cfg.DatabasePath), "node_adapters.db")
	nodeAdapterSvc := nodeadapter.NewService(nodeAdapterDBPath, logger)
	defer nodeAdapterSvc.Close()

	// Start sing-box node adapters first. VoWiFi/wificalling depends on the
	// SOCKS5 proxies provided by node adapters, so they must be ready before
	// the VoWiFi runtime starts.
	nodeAdapterSvc.StartEnabled(pollContext)

	vowifiManager, err := configureVoWiFiRuntime(
		deviceStartupContext,
		logger,
		database,
		deviceManager,
		cardReaders,
		func(ctx context.Context, call ims.ReceivedCall) error {
			if onIncomingCall != nil {
				return onIncomingCall(ctx, call)
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("configure VoWiFi runtime: %w", err)
	}
	deviceManager.SetOnHardwareKindChange(func(deviceID string) {
		vowifiManager.Reset(deviceID)
	})
	// Start background consumers only after the synchronous radio/VoWiFi
	// startup sequence. A snapshot refresh also takes the device operation
	// mutex; starting it earlier can strand cold boot forever behind a serial
	// read from an unstable USB enumeration.
	go pollDeviceSnapshots(pollContext, deviceLogger, database, deviceManager)
	go collectCellularTraffic(pollContext, logger, database)
	go persistLogsToStore(pollContext, logger, logs, database)
	if !developerEnabled {
		go disableAllDeveloperCellularData(pollContext, logger, database, deviceManager)
	} else {
		go watchDeveloperDisable(pollContext, logger, database, deviceManager, exportProxyManager, legacyExportProxyConfig)
	}
	go reconcileCardPolicies(pollContext, logger, database, deviceManager, vowifiManager)
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := vowifiManager.Close(stopContext); err != nil {
			logger.Warn("stop VoWiFi runtime", "error", err)
		}
	}()

	// MCP is opt-in. Read-only tokens receive a server variant whose tool list
	// omits write tools entirely.
	var mcpHandlers *server.MCPHandlers
	var switchESIM func(context.Context, string, string, string) error
	if cfg.MCPEnabled {
		readWrite, mcpErr := mcp.New(mcp.Options{
			Store:   database,
			Devices: deviceManager,
			VoWiFi:  vowifiManager,
			Logger:  logger,
			Version: buildinfo.Version,
			SwitchESIM: func(ctx context.Context, configuredID, iccid, aid string) error {
				if switchESIM == nil {
					return errors.New("profile switch service is not initialized")
				}
				return switchESIM(ctx, configuredID, iccid, aid)
			},
		})
		if mcpErr != nil {
			return fmt.Errorf("create MCP server: %w", mcpErr)
		}
		readOnly, mcpErr := mcp.New(mcp.Options{
			Store:    database,
			Devices:  deviceManager,
			VoWiFi:   vowifiManager,
			Logger:   logger,
			Version:  buildinfo.Version,
			ReadOnly: true,
		})
		if mcpErr != nil {
			return fmt.Errorf("create read-only MCP server: %w", mcpErr)
		}
		mcpHandlers = &server.MCPHandlers{
			ReadWrite: readWrite.Handler(),
			ReadOnly:  readOnly.Handler(),
		}
		logger.Info("MCP endpoint enabled at /mcp", "requires", "bearer api token")
	}

	handler, err := server.New(server.Options{
		Store:               database,
		Auth:                authService,
		Devices:             deviceManager,
		VoWiFi:              vowifiManager,
		Logs:                logs,
		Assets:              web.Dist,
		Logger:              logger,
		SecureCookies:       cfg.SecureCookies,
		MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		Extensions:          extensionManager,
		ExportProxy:         exportProxyManager,
		NodeAdapter:         nodeAdapterSvc,
		DeveloperEnabled:    developerEnabled,
		UpdateRepository:    strings.TrimSpace(os.Getenv("VONET_REPO")),
		UpdateToken:         strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		HTTPS:               httpsManager,
		MCP:                 mcpHandlers,
	})
	if err != nil {
		return err
	}
	defer handler.StopQQBotBinding()
	switchESIM = handler.SwitchESIMProfile

	onIncomingCall = func(ctx context.Context, call ims.ReceivedCall) error {
		deviceConfig, _ := database.Device(ctx, call.DeviceID)
		handler.NotifyIncomingCall(ctx, server.IncomingCallNotification{
			DeviceID:    call.DeviceID,
			DeviceName:  strings.TrimSpace(deviceConfig.Name),
			DeviceLabel: firstNonEmpty(deviceConfig.Name, deviceConfig.ID, "--"),
			Caller:      call.Caller,
			Called:      call.Called,
			Time:        call.Timestamp,
			Environment: "vowifi",
		})
		return nil
	}
	go handler.StartLogRetentionLoop(pollContext, time.Minute)
	handler.StartSSOCleanup(pollContext)
	go handler.StartSMSSyncLoop(pollContext, 15*time.Second)
	handler.StartCellularDataReconciler(pollContext)
	handler.StartTelegramBot(pollContext)
	handler.StartSMSNotificationDispatchers(pollContext)
	go handler.StartCellularCallMonitor(pollContext)
	handler.StartAutomaticTasks(pollContext)

	serverConfig := func(handler http.Handler) *http.Server {
		return &http.Server{
			Addr:              cfg.Address,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			// SSO provider verification has a 30-second budget and still needs
			// time to return a controlled callback or settings error.
			WriteTimeout:   40 * time.Second,
			IdleTimeout:    90 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
	}
	plainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpsManager.Enabled() {
			host := strings.TrimSpace(r.Host)
			if host == "" {
				host = cfg.Address
			}
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		handler.ServeHTTP(w, r)
	})
	plainServer := serverConfig(plainHandler)
	tlsServer := serverConfig(handler)
	baseListener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Address, err)
	}
	protocolMux := httpsmode.NewMultiplexer(baseListener, httpsManager)

	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()

	serverError := make(chan error, 2)
	go func() {
		logger.Info("HTTP server listening", "address", cfg.Address, "self_signed_https", httpsManager.Enabled())
		err := plainServer.Serve(protocolMux.Plain())
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverError <- err
	}()
	go func() {
		err := tlsServer.Serve(tls.NewListener(protocolMux.TLS(), httpsManager.TLSConfig()))
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverError <- err
	}()

	select {
	case err := <-serverError:
		cancelDeviceStartup()
		_ = protocolMux.Close()
		return err
	case <-signalContext.Done():
		// Stop delayed OpenStick 410 startup work before the deferred VoWiFi
		// manager shutdown begins, so it cannot enqueue a late enable request.
		cancelDeviceStartup()
		logger.Info("shutdown signal received")
	}
	// Long-lived SSE and polling handlers use this context. Stop them before
	// http.Server.Shutdown so they do not consume the entire graceful-shutdown
	// deadline while waiting for a stream that is intentionally still active.
	cancelPolling()

	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		cfg.ShutdownTimeout,
	)
	defer cancelShutdown()
	shutdownErrors := make(chan error, 2)
	go func() { shutdownErrors <- plainServer.Shutdown(shutdownContext) }()
	go func() { shutdownErrors <- tlsServer.Shutdown(shutdownContext) }()
	time.Sleep(10 * time.Millisecond)
	_ = protocolMux.Close()
	for range 2 {
		if err := <-shutdownErrors; err != nil {
			_ = plainServer.Close()
			_ = tlsServer.Close()
			return fmt.Errorf("graceful HTTP shutdown: %w", err)
		}
	}
	return nil
}

func configureDeviceBackends(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
) {
	configs, err := database.ListDevices(ctx)
	if err != nil {
		logger.Warn("configure device backends: list devices", "error", err)
		return
	}
	mapper := integration.ATMapper{Store: database, Devices: manager}
	for _, config := range configs {
		entry, mapErr := mapper.Get(config.ID)
		if mapErr != nil {
			// For PCSC readers, try to find the physical entry directly by
			// matching ReaderName against stored ControlDevice.
			if config.DeviceType == store.DeviceTypeUSBSIMReader {
				for _, e := range manager.List() {
					if !e.Discovered || e.Candidate.HardwareKind != pcsc.HardwareKind {
						continue
					}
					if matchPCSCDevice(config, e) {
						entry = e
						mapErr = nil
						break
					}
				}
			}
			if mapErr != nil {
				continue
			}
		}
		configChanged := server.MergeDeviceHardwareIdentity(&config, entry.Candidate)
		if entry.Candidate.HardwareKind == pcsc.HardwareKind {
			if err := manager.SetBackend(entry.ID, "pcsc"); err != nil {
				logger.Warn("configure PCSC backend", "device_id", config.ID, "error", err)
			}
			if err := manager.SetSIMPin(entry.ID, config.SIMPIN); err != nil {
				logger.Warn("configure USB SIM reader", "device_id", config.ID, "error", err)
			}
			// Ensure the stored ControlDevice matches the current SerialNumber so
			// that ATMapper.resolve and reconcileCardPolicies can find this reader.
			if strings.TrimSpace(config.ControlDevice) != strings.TrimSpace(entry.Candidate.SerialNumber) {
				config.ControlDevice = strings.TrimSpace(entry.Candidate.SerialNumber)
				configChanged = true
			}
			if configChanged {
				if err := database.UpsertDevice(ctx, config); err != nil {
					logger.Warn("repair USB SIM reader identity", "device_id", config.ID, "error", err)
				}
			}
			continue
		}
		if err := manager.SetBackend(entry.ID, config.DeviceBackend); err != nil {
			logger.Warn("configure device backend", "device_id", config.ID, "backend", config.DeviceBackend, "error", err)
		}
		if err := manager.SetESIMTransport(entry.ID, config.ESIMTransport); err != nil {
			logger.Warn("configure eSIM transport", "device_id", config.ID, "transport", config.ESIMTransport, "error", err)
		}
		if configChanged {
			if err := database.UpsertDevice(ctx, config); err != nil {
				logger.Warn("repair device hardware identity", "device_id", config.ID, "error", err)
			}
		}
	}
}

// restoreDefaultCellularRadios applies an explicitly saved cellular policy
// after restart. Missing policies remain RF-off and are claimed by the safe
// default policy; there is no automatic cellular fallback.
func restoreDefaultCellularRadios(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
) {
	configs, err := database.ListDevices(ctx)
	if err != nil {
		logger.Warn("startup cellular recovery: list devices", "error", err)
		return
	}
	mapper := integration.ATMapper{Store: database, Devices: manager}
	for _, config := range configs {
		entry, err := mapper.Get(config.ID)
		if err != nil {
			continue
		}
		if entry.Candidate.HardwareKind == pcsc.HardwareKind {
			continue
		}
		if entry.Snapshot == nil || !entry.Snapshot.FlightMode {
			continue
		}
		iccid := strings.TrimSpace(entry.Snapshot.ICCID)
		if iccid == "" {
			continue
		}
		if iccid != "" {
			policy, policyErr := database.CardPolicy(ctx, iccid)
			switch {
			case policyErr == nil && policy.AirplaneEnabled:
				continue
			case errors.Is(policyErr, store.ErrNotFound):
				continue
			case policyErr != nil && !errors.Is(policyErr, store.ErrNotFound):
				logger.Warn("startup cellular recovery: read card policy", "device_id", config.ID, "error", policyErr)
				continue
			}
		}
		restoreContext, cancel := startupFlightModeContext(ctx)
		_, err = manager.SetFlight(restoreContext, entry.ID, false)
		cancel()
		if err != nil {
			logger.Warn("startup cellular recovery failed", "device_id", config.ID, "error", err)
			continue
		}
		logger.Info("restored cellular radio after disabled VoWiFi", "device_id", config.ID, "iccid", iccid)
	}
}

func configuredCellularNetworkRequest(
	ctx context.Context,
	database *store.Store,
	config store.Device,
	snapshot *device.Snapshot,
) device.NetworkRequest {
	request := device.NetworkRequest{
		Enabled: true, APN: config.APN, IPVersion: "IPV4V6", Backend: config.DeviceBackend,
	}
	if snapshot == nil {
		return request
	}
	iccid := strings.TrimSpace(snapshot.ICCID)
	policy, err := database.CardPolicy(ctx, iccid)
	if err != nil {
		return request
	}
	request.APN = policy.APN
	if policy.IPVersion != "" {
		request.IPVersion = policy.IPVersion
	}
	profile, err := database.CardAPNProfileByAPN(ctx, iccid, policy.APN, policy.IPVersion)
	if err != nil {
		return request
	}
	request.Username = profile.Username
	request.Password = profile.Password
	request.Authentication = profile.AuthType
	if snapshot.RegistrationStatus == 5 && profile.RoamingIPVersion != "" {
		request.IPVersion = profile.RoamingIPVersion
	}
	return request
}

func disableAllDeveloperCellularData(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
) {
	configs, err := database.ListDevices(ctx)
	if err != nil {
		logger.Warn("developer cleanup: list devices", "error", err)
		return
	}
	mapper := integration.ATMapper{Store: database, Devices: manager}
	for _, config := range configs {
		entry, err := mapper.Get(config.ID)
		if err != nil {
			continue
		}
		if entry.Candidate.HardwareKind == pcsc.HardwareKind {
			continue
		}
		disableContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = manager.SetNetwork(disableContext, entry.ID, device.NetworkRequest{Enabled: false, Backend: config.DeviceBackend})
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("developer cleanup: stop cellular data", "device_id", config.ID)
		}
	}
}

func watchDeveloperDisable(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
	exportProxy *exportproxy.Manager,
	legacyConfigPath string,
) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if developer.Enabled(ctx, database) {
				continue
			}
			if exportProxy != nil {
				if err := exportProxy.DeleteAllAndDisable(ctx); err != nil && ctx.Err() == nil {
					logger.Warn("developer cleanup: delete export proxies", "error", err)
				}
			}
			if err := exportproxy.RemoveLegacyConfig(legacyConfigPath); err != nil {
				logger.Warn("developer cleanup: remove legacy export proxy configuration", "error", err)
			}
			if err := developer.ResetExperimental(ctx, database); err != nil && ctx.Err() == nil {
				logger.Warn("developer cleanup: reset settings", "error", err)
			}
			disableAllDeveloperCellularData(ctx, logger, database, manager)
			logger.Info("developer mode disabled; roaming data and export proxies were removed")
			return
		}
	}
}

func configureVoWiFiRuntime(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	deviceManager *device.Manager,
	cardReaders *pcsc.Service,
	onIncomingCall func(context.Context, ims.ReceivedCall) error,
) (*vowifiruntime.Manager, error) {
	mapper := integration.ATMapper{
		Store:   database,
		Devices: deviceManager,
	}
	atAdapterOptions := vowifi.ATModemAdapterOptions{
		// The test deployment is deliberately non-cellular. VoWiFi teardown
		// may restore CFUN, but it must never reactivate a PDP context.
		RestoreCellularData: false,
		// VoWiFi is always fail-closed with respect to cellular RF. Its teardown
		// leaves CFUN=4; only the explicit airplane-mode-off endpoint restores
		// CFUN=1.
		PureAirplanePolicy: func(deviceID string) bool {
			deviceConfig, configErr := database.Device(context.Background(), deviceID)
			return configErr == nil && deviceConfig.VoWiFiEnabled
		},
		HomePLMN: func(_ string, iccid, _ string) (string, string, bool) {
			return vowifi.HomePLMNFromICCID(iccid)
		},
	}
	ec20Adapter, err := vowifi.NewEC20Adapter(mapper, atAdapterOptions)
	if err != nil {
		return nil, err
	}
	zxicAdapter, err := vowifi.NewZXICAdapter(mapper, atAdapterOptions)
	if err != nil {
		return nil, err
	}
	nativeQMIAdapter, err := vowifi.NewNativeQMIAdapter(nativeQMIControllerMapper{Mapper: mapper, Devices: deviceManager}, func(deviceID string) bool {
		deviceConfig, configErr := database.Device(context.Background(), deviceID)
		return configErr == nil && deviceConfig.VoWiFiEnabled
	})
	if err != nil {
		return nil, err
	}
	pcscAdapter, err := vowifi.NewPCSCAdapter(cardReaders, func(ctx context.Context, deviceID string) (pcsc.Selector, string, error) {
		config, resolveErr := database.Device(ctx, strings.TrimSpace(deviceID))
		if resolveErr != nil {
			return pcsc.Selector{}, "", resolveErr
		}
		usbSerial := ""
		var extraMap map[string]any
		if len(config.Extra) > 0 {
			_ = json.Unmarshal(config.Extra, &extraMap)
		}
		if s, ok := extraMap["usb_serial_number"].(string); ok {
			usbSerial = strings.TrimSpace(s)
		}
		selector := pcsc.Selector{USBPath: config.USBPath, ReaderName: config.ControlDevice, USBSerial: usbSerial}
		return selector, config.SIMPIN, nil
	})
	pcscLeaseAdapter, err := vowifi.NewPCSCAdapterWithLeases(pcscAdapter, storePCSCIMEILeaseSource{store: database})
	if err != nil {
		return nil, err
	}
	deviceManager.SetPCSCIMEIProvider(pcscLeaseAdapter.CurrentIMEI)
	projector := integration.StateProjector{
		Store:   database,
		Devices: mapper,
	}
	manager := vowifiruntime.New(vowifiruntime.Options{
		Logger:  logger.With("category", "vowifi"),
		OnState: projector.Save,
		Factory: func(factoryContext context.Context, deviceID string) (*vowifi.Orchestrator, error) {
			deviceConfig, err := database.Device(factoryContext, deviceID)
			if err != nil {
				return nil, fmt.Errorf("load device %q VoWiFi config: %w", deviceID, err)
			}
			// Prefer the live HardwareKind from the device manager over the
			// persisted DeviceType in the database. When a user swaps hardware
			// (e.g. EC20 to card reader) reusing the same device ID, the DB
			// still holds the old DeviceType. The live HardwareKind reflects
			// the currently connected hardware.
			adapter := voWiFiAdapterForDevice(
				deviceConfig.DeviceType,
				func(lookupContext context.Context) (device.Device, error) {
					physicalID, resolveErr := integration.ResolvePhysicalID(
						lookupContext,
						database,
						deviceManager,
						deviceConfig.ID,
					)
					if resolveErr != nil {
						return device.Device{}, resolveErr
					}
					return deviceManager.Get(physicalID)
				},
				ec20Adapter,
				zxicAdapter,
				pcscLeaseAdapter,
				nativeQMIAdapter,
			)
			hardwareKind := deviceManager.HardwareKindFor(deviceID)
			if hardwareKind == pcsc.HardwareKind || (hardwareKind == "" && deviceConfig.DeviceType == store.DeviceTypeUSBSIMReader) {
				adapter = pcscLeaseAdapter
			} else if hardwareKind == "wwan" ||
				(hardwareKind == "" && deviceConfig.DeviceType == store.DeviceTypeWiFi410) {
				adapter = nativeQMIAdapter
			} else if hardwareKind != "" &&
				(deviceConfig.DeviceType == store.DeviceTypeUSBSIMReader ||
					deviceConfig.DeviceType == store.DeviceTypeWiFi410) {
				adapter = newResolvedATVoWiFiAdapter(
					deviceConfig.DeviceType,
					func(lookupContext context.Context) (device.Device, error) {
						physicalID, resolveErr := integration.ResolvePhysicalID(
							lookupContext,
							database,
							deviceManager,
							deviceConfig.ID,
						)
						if resolveErr != nil {
							return device.Device{}, resolveErr
						}
						return deviceManager.Get(physicalID)
					},
					ec20Adapter,
					zxicAdapter,
				)
			}
			return newVoWiFiOrchestrator(factoryContext, deviceConfig, database, adapter, logger, onIncomingCall)
		},
	})

	configured, err := database.ListDevices(ctx)
	if err != nil {
		_ = manager.Close(context.Background())
		return nil, err
	}
	for _, deviceConfig := range configured {
		if err := manager.Ensure(ctx, deviceConfig.ID); err != nil {
			_ = manager.Close(context.Background())
			return nil, fmt.Errorf("register device %q VoWiFi runtime: %w", deviceConfig.ID, err)
		}
		// Starting service is card-gated. A saved device is only a hardware
		// host and must wait for a manually registered, stably bound ICCID.
	}
	return manager, nil
}

const (
	vowifiStartupRadioAttempts = 3
	vowifiStartupRadioDelay    = time.Second
	wifi410VoWiFiStartupDelay  = 80 * time.Second
)

// requestVoWiFiStartup delays only the persisted startup policy for OpenStick
// 410 devices. Their Qualcomm UIM and Vodafone ePDG path need a short quiet
// period after a cold boot; user-triggered reconnects and every other device
// type continue to execute immediately.
func requestVoWiFiStartup(
	ctx context.Context,
	logger *slog.Logger,
	deviceType string,
	deviceID string,
	delay time.Duration,
	request func() error,
) error {
	if deviceType != store.DeviceTypeWiFi410 || delay <= 0 {
		return request()
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info(
		"OpenStick 410 VoWiFi startup delayed",
		"device_id", deviceID,
		"delay", delay,
	)
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := request(); err != nil {
			logger.Warn(
				"OpenStick 410 delayed VoWiFi startup failed",
				"device_id", deviceID,
				"error", err,
			)
		}
	}()
	return nil
}

func shouldDelayWiFi410VoWiFi(deviceType string, now, notBefore time.Time) bool {
	return deviceType == store.DeviceTypeWiFi410 && now.Before(notBefore)
}

type flightModeSetter interface {
	SetFlight(context.Context, string, bool) (device.FlightResult, error)
}

func protectVoWiFiStartupRadio(ctx context.Context, manager flightModeSetter, physicalID string) error {
	return protectVoWiFiStartupRadioWithRetry(
		ctx,
		manager,
		physicalID,
		vowifiStartupRadioAttempts,
		vowifiStartupRadioDelay,
	)
}

func protectVoWiFiStartupRadioWithRetry(
	ctx context.Context,
	manager flightModeSetter,
	physicalID string,
	attempts int,
	delay time.Duration,
) error {
	if attempts <= 0 {
		return nil
	}
	retryBudget := time.Duration(attempts)*flightModeTransitionTimeout + time.Duration(attempts-1)*delay
	retryContext, cancelRetry := context.WithTimeout(ctx, retryBudget)
	defer cancelRetry()
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		flightContext, cancel := context.WithTimeout(retryContext, flightModeTransitionTimeout)
		_, lastErr = manager.SetFlight(flightContext, physicalID, true)
		cancel()
		if lastErr == nil {
			return nil
		}
		if attempt+1 == attempts {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-retryContext.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return errors.Join(lastErr, retryContext.Err())
		case <-timer.C:
		}
	}
	return lastErr
}

type vowifiDeviceAdapter interface {
	vowifi.SIMIdentityReader
	vowifi.AKAProvider
	vowifi.PreferredAKAProvider
	vowifi.RadioController
}

// receivedIMSSMSMessageID returns a session-independent, subscription-scoped
// identity for an IMS SMS.
func receivedIMSSMSMessageID(message ims.ReceivedSMS) string {
	if message.Concat == nil || message.Concat.Total <= 1 {
		return message.MessageID
	}
	subscriptionID := firstNonEmpty(
		strings.TrimSpace(message.ICCID),
		strings.TrimSpace(message.IMSI),
	)
	if subscriptionID == "" {
		// Without a subscription identity, reusing the concat reference after an
		// eSIM switch is indistinguishable from a later segment. Keep the IMS
		// delivery identity instead of risking a cross-profile merge.
		return message.MessageID
	}
	// A segment of a carrier-split long SMS over IMS. Address the whole
	// message by the configured device rather than the current IMS session's
	// reported IMEI, and include the subscription so a later eSIM profile cannot
	// reuse the same sender/reference tuple and merge into the old message.
	fingerprint := sha256.Sum256([]byte(subscriptionID))
	deviceSubscription := message.DeviceID + ":subscription:" + hex.EncodeToString(fingerprint[:])
	return store.StableConcatMessageID(
		"ims", "", deviceSubscription, message.From,
		message.Concat.Reference, message.Concat.Total,
	)
}

func newVoWiFiOrchestrator(
	ctx context.Context,
	deviceConfig store.Device,
	database *store.Store,
	adapter vowifiDeviceAdapter,
	logger *slog.Logger,
	onIncomingCall func(context.Context, ims.ReceivedCall) error,
) (*vowifi.Orchestrator, error) {
	// The ePDG tunnel is an IMS service: request the dedicated IMS APN and never
	// the cellular data APN cached on the device or card policy.
	apn := vowifisettings.IMSAPN(ctx, database)
	vowifiLogger := logger.With("category", "vowifi", "device_id", deviceConfig.ID)
	tunnelProvider, err := ike.NewProvider(ike.Config{
		APN: apn, Logger: vowifiLogger, AutoProposalFallback: true,
	})
	if err != nil {
		return nil, fmt.Errorf("device %q IKE provider: %w", deviceConfig.ID, err)
	}
	imsProvider, err := ims.NewProvider(adapter, ims.Config{
		MTUCompatibility: func(ctx context.Context) bool {
			return vowifisettings.MTUCompatibility(ctx, database)
		},
		Logger: vowifiLogger,
		// Carrier-specific transport and SMSC defaults live in the shared data
		// profile. Prefer network-provided P-CSCF hints, then safely try the
		// alternate transport only if no SIP response was observed.
		Transport:             "tcp",
		AutoTransportFallback: true,
		OnIncomingCall:        onIncomingCall,
		OnSMS: func(ctx context.Context, message ims.ReceivedSMS) error {
			localPhone, _ := database.PhoneNumberForICCID(ctx, message.ICCID)
			modemIMEI := firstNonEmpty(message.ModemIMEI, deviceConfig.ModemIMEI)
			metadata := map[string]any{
				"transport":                "ims",
				"encoding":                 message.Encoding,
				"concat":                   message.Concat,
				"rp_reference":             message.RPReference,
				"call_id":                  message.CallID,
				"received_at":              message.Timestamp,
				"service_center_timestamp": message.ServiceCenterTimestamp,
				"raw_rpdu":                 message.RawRPDU,
				"raw_tpdu":                 message.RawTPDU,
				"decode_error":             message.DecodeError,
			}
			if message.TimestampCorrection != nil {
				metadata["service_center_timestamp_correction"] = message.TimestampCorrection
			}
			extra, _ := json.Marshal(metadata)
			partsTotal := 1
			if message.Concat != nil && message.Concat.Total > 0 {
				partsTotal = message.Concat.Total
			}
			messageID := receivedIMSSMSMessageID(message)
			_, saveErr := database.SaveSMSMessage(ctx, store.SMSMessage{
				MessageID:  messageID,
				DeviceID:   message.DeviceID,
				ModemIMEI:  modemIMEI,
				ICCID:      message.ICCID,
				IMSI:       message.IMSI,
				LocalPhone: localPhone,
				Peer:       message.From,
				Direction:  "inbound",
				Body:       message.Text,
				Timestamp:  message.Timestamp,
				Status:     "received",
				Source:     "ims",
				PartsTotal: partsTotal,
				Read:       false,
				Extra:      extra,
			})
			return saveErr
		},
		OnSMSStatus: func(ctx context.Context, report ims.ReceivedSMSStatus) error {
			deliveryReport := store.SMSDeliveryReport{
				DeviceID:          report.DeviceID,
				ModemIMEI:         firstNonEmpty(report.ModemIMEI, deviceConfig.ModemIMEI),
				IMSI:              report.IMSI,
				Peer:              report.To,
				Source:            "ims",
				MessageReference:  report.MessageReference,
				StatusCode:        report.StatusCode,
				DeliveryState:     report.DeliveryStatus,
				ServiceCenterTime: report.ServiceCenterTimestamp,
				DischargeTime:     report.DischargeTimestamp,
				ReceivedAt:        report.Timestamp,
			}
			if report.TimestampCorrection != nil {
				deliveryReport.Extra = map[string]any{
					"service_center_timestamp_correction": report.TimestampCorrection,
				}
			}
			var applyErr error
			for attempt := 0; attempt < 10; attempt++ {
				_, applyErr = database.ApplySMSDeliveryReport(ctx, deliveryReport)
				if !errors.Is(applyErr, store.ErrNotFound) {
					return applyErr
				}
				// A status report can race the API handler persisting the SIP 202
				// result. Give that write a brief chance to complete.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
			// A late report from before this process started must still be
			// acknowledged, otherwise the SMSC will keep retransmitting it.
			return nil
		},
		OnUSSD: func(ctx context.Context, message ims.ReceivedUSSD) error {
			localPhone, _ := database.PhoneNumberForICCID(ctx, message.ICCID)
			extra, _ := json.Marshal(map[string]any{
				"transport":   "ims-ussd",
				"dcs":         message.DCS,
				"call_id":     message.CallID,
				"received_at": message.Timestamp,
				"raw_body":    message.RawBody,
			})
			_, saveErr := database.SaveSMSMessage(ctx, store.SMSMessage{
				MessageID:  message.MessageID,
				DeviceID:   message.DeviceID,
				ModemIMEI:  deviceConfig.ModemIMEI,
				ICCID:      message.ICCID,
				IMSI:       message.IMSI,
				LocalPhone: localPhone,
				Peer:       message.From,
				Direction:  "inbound",
				Body:       message.Text,
				Timestamp:  message.Timestamp,
				Status:     "received",
				Source:     "ims-ussd",
				PartsTotal: 1,
				Read:       false,
				Extra:      extra,
			})
			return saveErr
		},
	})
	if err != nil {
		return nil, fmt.Errorf("device %q IMS provider: %w", deviceConfig.ID, err)
	}
	orchestrator, err := vowifi.New(vowifi.Dependencies{
		SIM:         adapter,
		AKA:         adapter,
		Radio:       adapter,
		Proxy:       integration.ProxyResolver{Store: database},
		Tunnel:      tunnelProvider,
		IMS:         imsProvider,
		Phones:      integration.PhoneStore{Store: database, DeviceID: deviceConfig.ID},
		RouteStates: database,
	}, vowifi.Options{
		DeviceID:           deviceConfig.ID,
		AllowIMSWithoutSMS: true,
		SIMStabilizeDelay:  500 * time.Millisecond,
	})
	if err != nil {
		return nil, fmt.Errorf("device %q VoWiFi orchestrator: %w", deviceConfig.ID, err)
	}
	return orchestrator, nil
}

func provisionDiscoveredDevices(
	ctx context.Context,
	database *store.Store,
	manager *device.Manager,
) error {
	configured, err := database.ListDevices(ctx)
	if err != nil {
		return err
	}
	provisionErrors := make([]error, 0)
	for _, config := range configured {
		if store.NormalizeDeviceType(config.DeviceType) != store.DeviceTypeUSBSIMReader {
			continue
		}
		if _, leaseErr := database.PCSCIMEILease(ctx, config.ID); leaseErr == nil {
			continue
		} else if !errors.Is(leaseErr, store.ErrNotFound) {
			provisionErrors = append(provisionErrors, fmt.Errorf("read reader %q IMEI lease: %w", config.ID, leaseErr))
			continue
		}
		if _, repairErr := database.EnsurePCSCIMEILease(ctx, config.ID); repairErr != nil {
			provisionErrors = append(provisionErrors, fmt.Errorf("repair reader %q IMEI lease: %w", config.ID, repairErr))
		}
	}
	if len(configured) != 0 {
		return errors.Join(provisionErrors...)
	}
	for _, discovered := range manager.List() {
		candidate := discovered.Candidate
		if candidate.DiscoveryIssue != "" ||
			(!candidate.HasATPort() && candidate.HardwareKind != pcsc.HardwareKind) {
			continue
		}
		deviceType := provisionedDeviceType(candidate)
		backend := "at"
		control := candidate.ATPort.OpenPath()
		esimTransport := backend
		if candidate.QMIControl != "" {
			backend = "qmi"
			control = candidate.QMIControl
			esimTransport = backend
		}
		if candidate.HardwareKind == pcsc.HardwareKind {
			backend = "pcsc"
			control = strings.TrimSpace(candidate.SerialNumber)
			deviceType = store.DeviceTypeUSBSIMReader
			esimTransport = "pcsc"
		}
		modemIMEI := ""
		// For DJI/Baiwang devices, read IMEI to use as device ID.
		// Try AT port first, then fall back to QMI DMS for devices without
		// a serial port (e.g. DJI 4G modules using Quectel EC20 over QMI only).
		if modem.IsDJI4GUSB(candidate.VendorID, candidate.ProductID) || modem.IsBaiwangDevice(candidate.Product) {
			imei := modem.ReadIMEIFromAT(candidate.ATPort.OpenPath())
			if imei == "" && candidate.QMIControl != "" {
				if qmiIMEI, qmiErr := manager.ReadQMIIMEI(candidate); qmiErr == nil {
					imei = qmiIMEI
				}
			}
			if imei != "" {
				discovered.ID = "Baiwang-" + imei
				control = imei
				modemIMEI = imei
			}
		}
		name := candidate.Product
		if modem.IsML307(candidate) &&
			(name == "" || strings.EqualFold(name, "Android") || strings.EqualFold(name, "Linux")) {
			name = "ML307"
		} else if name == "" || strings.EqualFold(name, "Android") {
			name = "Quectel EC20 / EC25"
		}
		supportsSMS := deviceType != store.DeviceTypeWiFi410
		config := store.Device{
			ID:             discovered.ID,
			Name:           name,
			DeviceType:     deviceType,
			Interface:      candidate.NetworkInterface,
			ControlDevice:  control,
			ModemIMEI:      modemIMEI,
			ATPort:         candidate.ATPort.OpenPath(),
			USBPath:        candidate.USBPath,
			ProxyPort:      1080,
			BaudRate:       115200,
			DataBits:       8,
			StopBits:       1,
			Parity:         "none",
			DeviceBackend:  backend,
			ESIMTransport:  esimTransport,
			NetworkEnabled: false,
			SMSEnabled:     supportsSMS,
			VoWiFiEnabled:  true,
		}
		server.MergeDeviceHardwareIdentity(&config, candidate)

		var err error
		if deviceType == store.DeviceTypeUSBSIMReader {
			_, _, err = database.CreateDeviceWithPCSCIMEILease(ctx, config, store.PCSCIMEILeaseRequest{Mode: "random"})
			if errors.Is(err, store.ErrPCSCIMEIPoolExhausted) {
				provisionErrors = append(provisionErrors, fmt.Errorf("skip reader %q: %w", config.ID, err))
				continue
			}
		} else {
			err = database.UpsertDevice(ctx, config)
		}
		if err != nil {
			provisionErrors = append(provisionErrors, fmt.Errorf("provision device %q: %w", config.ID, err))
		}
	}
	return errors.Join(provisionErrors...)
}

func provisionedDeviceType(candidate modem.Candidate) string {
	if modem.ChipsetForUSB(candidate.VendorID, candidate.ProductID) == modem.ChipsetZXIC {
		return store.DeviceTypeZXIC
	}
	if modem.IsML307(candidate) {
		return store.DeviceTypeML307
	}
	controlName := filepath.Base(filepath.Clean(candidate.QMIControl))
	if candidate.HardwareKind == "wwan" &&
		strings.HasPrefix(controlName, "wwan") && strings.Contains(controlName, "qmi") {
		return store.DeviceTypeWiFi410
	}
	if modem.IsDJI4GUSB(candidate.VendorID, candidate.ProductID) || modem.IsBaiwangDevice(candidate.Product) {
		return store.DeviceTypeDJI4G
	}
	return store.DeviceTypePCIeEC20EC25
}

// persistLogsToStore subscribes to the live log hub and durably appends every
// entry to the log_events table, so runtime logs survive restarts and can be
// pruned by the configured retention policy.
func persistLogsToStore(
	ctx context.Context,
	logger *slog.Logger,
	logs *loghub.Hub,
	database *store.Store,
) {
	entries, cancel := logs.Subscribe(512)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			if loghub.IsHTTPAccessEntry(entry) {
				continue
			}
			entry = loghub.SanitizeEntry(entry)
			var fields json.RawMessage
			if len(entry.Fields) > 0 {
				if raw, err := json.Marshal(entry.Fields); err == nil {
					fields = raw
				}
			}
			if _, err := database.AppendLogEvent(ctx, store.LogEvent{
				Time:    entry.Time,
				Level:   entry.Level,
				Message: entry.Message,
				Caller:  entry.Caller,
				Fields:  fields,
			}); err != nil && ctx.Err() == nil {
				logger.Warn("persist log event failed", "error", err)
			}
		}
	}
}

func pollDeviceSnapshots(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
) {
	recoveryTracker := newRegistrationRecoveryTracker(ec20RegistrationGrace, ec20RegistrationEscalation)
	recoverRegistration := func(entry device.Device, snapshot device.Snapshot, action registrationRecoveryAction) {
		if action == registrationRecoveryNone {
			return
		}
		go func() {
			recoveryContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()
			expectedICCID := strings.TrimSpace(snapshot.ICCID)
			issued := runRegistrationRecovery(
				recoveryContext,
				logger,
				database,
				manager,
				entry.ID,
				expectedICCID,
				action,
			)
			recoveryTracker.complete(entry.ID, expectedICCID, action, issued, time.Now())
		}()
	}
	lastFullRefresh := time.Time{}
	refresh := func() {
		fullRefresh := time.Since(lastFullRefresh) >= 30*time.Second
		if fullRefresh {
			lastFullRefresh = time.Now()
			discoveryContext, cancelDiscovery := context.WithTimeout(ctx, 10*time.Second)
			_, err := manager.Discover(discoveryContext)
			cancelDiscovery()
			if err != nil {
				logger.Debug("periodic modem discovery failed", "error", err)
				return
			}
			// Hotplug can replace the physical discovery ID. Rebind each configured
			// device's selected QMI/AT control plane before collecting its snapshot.
			configureDeviceBackends(ctx, logger, database, manager)
		}
		entries := manager.List()
		// Each physical modem owns its own operation lock. Refresh them in
		// parallel so a slow or wedged EC20 on one hub port cannot delay signal
		// and identity updates for every other modem by 30 seconds at a time.
		var refreshGroup sync.WaitGroup
		refreshSlots := make(chan struct{}, 4)
		for _, entry := range entries {
			if !entry.Discovered ||
				entry.Candidate.DiscoveryIssue != "" ||
				(!entry.Candidate.HasATPort() && entry.Candidate.HardwareKind != pcsc.HardwareKind) {
				continue
			}
			// Native 410 firmware can miss physical SIM insertion events. Probe it
			// every five seconds; retain normal discovery/other modem cadence.
			if !fullRefresh && provisionedDeviceType(entry.Candidate) != store.DeviceTypeWiFi410 {
				continue
			}
			entry := entry
			refreshGroup.Add(1)
			go func() {
				defer refreshGroup.Done()
				select {
				case refreshSlots <- struct{}{}:
					defer func() { <-refreshSlots }()
				case <-ctx.Done():
					return
				}
				refreshContext, cancelRefresh := context.WithTimeout(ctx, 30*time.Second)
				snapshot, refreshErr := manager.Refresh(refreshContext, entry.ID)
				cancelRefresh()
				if refreshErr != nil && ctx.Err() == nil {
					if errors.Is(refreshErr, device.ErrESIMRecovering) || errors.Is(refreshErr, remoteproc.ErrUnavailable) {
						logger.Debug("modem snapshot refresh deferred", "device_id", entry.ID, "error", refreshErr)
					} else {
						logger.Warn("modem snapshot refresh failed", "device_id", entry.ID, "error", refreshErr)
					}
				}
				if refreshErr == nil && ctx.Err() == nil {
					enforceCardRegion(ctx, logger, database, manager, entry.ID, &snapshot)
					enforceUnregisteredCardSafety(ctx, logger, database, manager, entry.ID, &snapshot)
					recoverRegistration(entry, snapshot, recoveryTracker.observe(entry.ID, &snapshot, time.Now()))
				}
			}()
		}
		refreshGroup.Wait()
	}
	refresh()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// enforceUnregisteredCardSafety protects an unknown SIM without registering
// it or creating policy state. Service remains suspended until the user saves
// the ICCID in card management and a stable binding becomes active.
func enforceUnregisteredCardSafety(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
	physicalID string,
	snapshot *device.Snapshot,
) {
	if snapshot == nil || !snapshot.SIMReady || strings.TrimSpace(snapshot.ICCID) == "" ||
		device.RegionBlockReason(snapshot.IMSI) != "" {
		return
	}
	iccid := strings.TrimSpace(snapshot.ICCID)
	if _, err := database.SimCard(ctx, iccid); err == nil {
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		logger.Warn("unregistered card safety: read card registry", "iccid", iccid, "error", err)
		return
	}
	flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
	_, err := manager.SetFlight(flightContext, physicalID, true)
	cancel()
	if err != nil {
		logger.Warn("unregistered card safety: failed to establish airplane mode", "device_id", physicalID, "iccid", iccid, "error", err)
	}
	mapper := integration.ATMapper{Store: database, Devices: manager}
	configs, err := database.ListDevices(ctx)
	if err != nil {
		return
	}
	for _, config := range configs {
		entry, mapErr := mapper.Get(config.ID)
		if mapErr != nil || entry.ID != physicalID {
			continue
		}
		config.NetworkEnabled = false
		config.VoWiFiEnabled = false
		if err := database.UpsertDevice(ctx, config); err != nil {
			logger.Warn("unregistered card safety: update device state", "device_id", config.ID, "error", err)
		}
		break
	}
	logger.Info("unregistered SIM protected in airplane mode", "device_id", physicalID, "iccid", iccid)
}

func reconcileCardPoliciesLegacy(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
	vowifiManager *vowifiruntime.Manager,
) {
	observedCards := make(map[string]string)
	wifi410StartupNotBefore := time.Now().Add(wifi410VoWiFiStartupDelay)
	reconcile := func() {
		policies, policyListErr := database.ListCardPolicies(ctx)
		if policyListErr == nil {
			for _, policy := range policies {
				if !policy.VoWiFiEnabled || (policy.AirplaneEnabled && !policy.NetworkEnabled) {
					continue
				}
				policy.AirplaneEnabled = true
				policy.NetworkEnabled = false
				if err := database.UpsertCardPolicy(ctx, policy); err != nil {
					logger.Warn("reconcile card policy: normalize stored RF-safe VoWiFi policy", "iccid", policy.ICCID, "error", err)
				}
			}
		}
		configs, err := database.ListDevices(ctx)
		if err != nil {
			return
		}
		mapper := integration.ATMapper{Store: database, Devices: manager}
		for _, config := range configs {
			entry, mapErr := mapper.Get(config.ID)
			hotSwappableSIM := config.DeviceType == store.DeviceTypeUSBSIMReader || config.DeviceType == store.DeviceTypeWiFi410
			if mapErr != nil || entry.Snapshot == nil {
				// For PCSC readers, try direct ReaderName matching as fallback.
				if config.DeviceType == store.DeviceTypeUSBSIMReader && mapErr != nil {
					for _, e := range manager.List() {
						if !e.Discovered || e.Candidate.HardwareKind != pcsc.HardwareKind {
							continue
						}
						if matchPCSCDevice(config, e) && e.Snapshot != nil {
							entry = e
							mapErr = nil
							break
						}
					}
				}
				if mapErr != nil || entry.Snapshot == nil {
					if hotSwappableSIM && observedCards[config.ID] != "missing" {
						if state, stateErr := vowifiManager.State(config.ID); stateErr == nil && state.ICCID != "" {
							if state.Phase == "idle" || state.Phase == "failed" {
								_, _ = vowifiManager.RequestReconnect(config.ID)
							}
						}
					}
					// Device is offline: always update database and disable runtime if needed.
					if observedCards[config.ID] != "missing" {
						// Always update database to reflect offline state.
						if config.VoWiFiEnabled {
							config.VoWiFiEnabled = false
							if err := database.UpsertDevice(ctx, config); err != nil {
								logger.Warn("device offline: update database VoWiFi state", "device_id", config.ID, "error", err)
							}
						}
						// Disable runtime if still enabled.
						if state, stateErr := vowifiManager.State(config.ID); stateErr == nil && state.Enabled {
							_, _ = vowifiManager.RequestEnabled(config.ID, false)
							logger.Info("device offline, disabled WiFiCalling", "device_id", config.ID)
						}
						observedCards[config.ID] = "missing"
					}
					continue
				}
			}
			iccid := strings.TrimSpace(entry.Snapshot.ICCID)
			if iccid == "" {
				if hotSwappableSIM && observedCards[config.ID] != "missing" {
					if state, stateErr := vowifiManager.State(config.ID); stateErr == nil && state.ICCID != "" {
						if state.Phase == "idle" || state.Phase == "failed" {
							_, _ = vowifiManager.RequestReconnect(config.ID)
						}
					}
					observedCards[config.ID] = "missing"
				}
				continue
			}
			previousObserved := observedCards[config.ID]
			observedCards[config.ID] = iccid
			policy, policyErr := database.CardPolicy(ctx, iccid)
			if policyErr != nil {
				continue
			}
			if policy.VoWiFiEnabled && (!policy.AirplaneEnabled || policy.NetworkEnabled) {
				policy.AirplaneEnabled = true
				policy.NetworkEnabled = false
				if err := database.UpsertCardPolicy(ctx, policy); err != nil {
					logger.Warn("reconcile card policy: normalize RF-safe VoWiFi policy", "device_id", config.ID, "iccid", iccid, "error", err)
					continue
				}
			}
			deviceChanged := false
			if config.VoWiFiEnabled != policy.VoWiFiEnabled || (policy.VoWiFiEnabled && config.NetworkEnabled) {
				config.VoWiFiEnabled = policy.VoWiFiEnabled
				if policy.VoWiFiEnabled {
					config.NetworkEnabled = false
				}
				deviceChanged = true
			}
			if config.APN != strings.TrimSpace(policy.APN) {
				config.APN = strings.TrimSpace(policy.APN)
				deviceChanged = true
			}
			if deviceChanged {
				if err := database.UpsertDevice(ctx, config); err != nil {
					logger.Warn("reconcile card policy: update device", "device_id", config.ID, "error", err)
					continue
				}
			}
			state, stateErr := vowifiManager.State(config.ID)
			if policy.VoWiFiEnabled {
				if !entry.Snapshot.FlightMode {
					flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
					_, _ = manager.SetFlight(flightContext, entry.ID, true)
					cancel()
				}
				switch {
				case stateErr != nil || !state.Enabled:
					if shouldDelayWiFi410VoWiFi(config.DeviceType, time.Now(), wifi410StartupNotBefore) {
						continue
					}
					_, _ = vowifiManager.RequestEnabled(config.ID, true)
				case state.ICCID != "" && !strings.EqualFold(strings.TrimSpace(state.ICCID), iccid) && (state.Phase == "idle" || state.Phase == "failed"):
					_, _ = vowifiManager.RequestReconnect(config.ID)
				case hotSwappableSIM && previousObserved == "missing" && (state.Phase == "idle" || state.Phase == "failed"):
					_, _ = vowifiManager.RequestReconnect(config.ID)
				}
				continue
			}
			if stateErr == nil && state.Enabled {
				_, _ = vowifiManager.RequestEnabled(config.ID, false)
				continue
			}
			if policy.AirplaneEnabled != entry.Snapshot.FlightMode {
				flightContext, cancel := context.WithTimeout(ctx, flightModeTransitionTimeout)
				_, _ = manager.SetFlight(flightContext, entry.ID, policy.AirplaneEnabled)
				cancel()
			}
		}
	}
	reconcile()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

// pcscReaderSerial returns the USB serial number for a PCSC device config or entry.
func pcscReaderSerial(entry device.Device) string {
	return strings.TrimSpace(entry.Candidate.SerialNumber)
}

// matchPCSCDevice checks if a physical PCSC entry matches a configured device.
// It uses USB serial number (stored in ControlDevice) for stable matching.
func matchPCSCDevice(config store.Device, entry device.Device) bool {
	return entry.Candidate.HardwareKind == pcsc.HardwareKind && pcscEntryMatchesForStartup(entry, config.ID, strings.TrimSpace(config.ControlDevice), strings.TrimSpace(config.USBPath))
}

// cardPolicySourceRegionBlock marks a card policy that was written automatically
// because the inserted SIM belongs to a region the product does not serve. It
// doubles as the persistent record that the radio was forced off by us, so the
// block survives restarts and can be lifted when an allowed card is detected.
const cardPolicySourceRegionBlock = "auto_region_block"

// enforceCardRegion applies the regional service policy for one refreshed
// device. When BlockedMCCs contains the SIM's home MCC, the radio is forced
// into airplane mode and a blocking card policy is persisted. Currently no
// regions are blocked. The check is fail-open — it only acts on a positively
// read blocked IMSI — and the lift path only runs once the current card is
// positively confirmed to be allowed, so an unreadable IMSI never causes a
// block or a spurious restore.
func enforceCardRegion(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
	id string,
	snapshot *device.Snapshot,
) {
	if snapshot == nil || !snapshot.SIMReady {
		return
	}
	imsi := strings.TrimSpace(snapshot.IMSI)
	if imsi == "" {
		// Region unknown: hold the current state rather than block or restore.
		return
	}
	if reason := device.RegionBlockReason(imsi); reason != "" {
		if !snapshot.FlightMode {
			flightContext, cancelFlight := context.WithTimeout(ctx, flightModeTransitionTimeout)
			_, err := manager.SetFlight(flightContext, id, true)
			cancelFlight()
			if err != nil && ctx.Err() == nil {
				logger.Warn(
					"region block: failed to force airplane mode",
					"device_id", id, "error", err,
				)
			}
		}
		if snapshot.ICCID != "" {
			policy, policyErr := database.CardPolicy(ctx, snapshot.ICCID)
			if errors.Is(policyErr, store.ErrNotFound) {
				policy = store.CardPolicy{ICCID: snapshot.ICCID, IPVersion: "IPV4V6"}
				policyErr = nil
			}
			policy.NetworkEnabled = false
			policy.VoWiFiEnabled = false
			policy.AirplaneEnabled = true
			policy.Source = cardPolicySourceRegionBlock
			if policyErr != nil && ctx.Err() == nil {
				logger.Warn("region block: failed to read card policy", "device_id", id, "iccid", snapshot.ICCID, "error", policyErr)
			} else if err := database.UpsertCardPolicy(ctx, policy); err != nil && ctx.Err() == nil {
				logger.Warn(
					"region block: failed to persist card policy",
					"device_id", id, "iccid", snapshot.ICCID, "error", err,
				)
			}
		}
		logger.Warn(
			"blocked SIM detected; service disabled and radio forced off",
			"device_id", id, "iccid", snapshot.ICCID, "imsi", imsi, "reason", reason,
		)
		return
	}
	liftCardRegionBlock(ctx, logger, database, manager, id, snapshot)
}

// liftCardRegionBlock removes the regional marker once an allowed SIM is
// confirmed and restores RF so the device can register on the network.
func liftCardRegionBlock(
	ctx context.Context,
	logger *slog.Logger,
	database *store.Store,
	manager *device.Manager,
	id string,
	snapshot *device.Snapshot,
) {
	policies, err := database.ListCardPolicies(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("region block: failed to list card policies", "error", err)
		}
		return
	}
	outstanding := make([]store.CardPolicy, 0, 1)
	for _, policy := range policies {
		if policy.Source == cardPolicySourceRegionBlock {
			outstanding = append(outstanding, policy)
		}
	}
	if len(outstanding) == 0 {
		return
	}
	for _, policy := range outstanding {
		if err := database.DeleteCardPolicy(ctx, policy.ICCID); err != nil && ctx.Err() == nil {
			logger.Warn(
				"region block: failed to clear auto policy",
				"iccid", policy.ICCID, "error", err,
			)
		}
	}
	// Restore RF so the SIM can register on the network and use VoWiFi.
	if snapshot.FlightMode {
		restoreCtx, cancelRestore := context.WithTimeout(ctx, flightModeTransitionTimeout)
		_, restoreErr := manager.SetFlight(restoreCtx, id, false)
		cancelRestore()
		if restoreErr != nil && ctx.Err() == nil {
			logger.Warn("region block: failed to restore RF after lifting block",
				"device_id", id, "error", restoreErr)
		}
	}
	logger.Info(
		"region block lifted; RF restored",
		"device_id", id, "iccid", snapshot.ICCID, "imsi", snapshot.IMSI,
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
