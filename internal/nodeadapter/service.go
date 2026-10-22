package nodeadapter

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"vonet/internal/proxy"
	"vonet/internal/singbox"
)

const (
	globalConfigKey       = "global_config"
	DefaultStartPort      = 50000
	DefaultHTTPStartPort  = 60000
	ProbeTimeout          = 10 * time.Second
	healthCheckInterval   = 30 * time.Second
	maxAutoRestarts       = 5
	restartBackoffBase    = 5 * time.Second
	restartBackoffMax     = 2 * time.Minute
	restartBackoffReset   = 5 * time.Minute // if process runs this long, reset counter

	maxStartupRetries = 5
	startupRetryDelay = 2 * time.Second
)

type GlobalConfig struct {
	ListenIP string `json:"listen_ip"`
	UDPIP    string `json:"udp_ip"`
}

// EnvStatus reports the readiness of the node-adapter subsystem.
type EnvStatus struct {
	DBPath     string `json:"db_path"`
	DBExists   bool   `json:"db_exists"`
	DBReady    bool   `json:"db_ready"`
	DBError    string `json:"db_error,omitempty"`
	SingboxOK  bool   `json:"singbox_ok"`
	SingboxMsg string `json:"singbox_msg,omitempty"`
}

// restartTracker tracks restart attempts for a single adapter.
type restartTracker struct {
	count       int
	lastRestart time.Time
	lastAlive   time.Time
}

type Service struct {
	dbPath  string
	db      *DB
	dbMu    sync.Mutex
	manager *singbox.Manager
	logger  *slog.Logger

	// Auto-restart tracking.
	restartMu   sync.Mutex
	restartInfo map[string]*restartTracker
	stopWatch   chan struct{}
}

// NewService creates a service that lazily opens the database on first use.
func NewService(dbPath string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	mgr := singbox.NewManager(logger)
	svc := &Service{
		dbPath:      dbPath,
		manager:     mgr,
		logger:      logger,
		restartInfo: make(map[string]*restartTracker),
		stopWatch:   make(chan struct{}),
	}
	// Register exit handler: auto-restart on unexpected exit.
	mgr.OnExit = svc.handleProcessExit
	return svc
}

// ensureDB opens the database if it has not been opened yet.
func (s *Service) ensureDB(ctx context.Context) (*DB, error) {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.db != nil {
		return s.db, nil
	}
	database, err := Open(ctx, s.dbPath)
	if err != nil {
		return nil, err
	}
	s.db = database
	return s.db, nil
}

// CreateDB explicitly creates the database file. Returns nil if it already exists.
func (s *Service) CreateDB(ctx context.Context) error {
	_, err := s.ensureDB(ctx)
	return err
}

// Close stops all running instances and closes the database.
func (s *Service) Close() {
	close(s.stopWatch)
	s.manager.StopAll()
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
}

// SingboxLogs returns the sing-box log buffer.
func (s *Service) SingboxLogs() *singbox.LogBuffer {
	return s.manager.Logs
}

// PauseAll stops all running sing-box processes. Adapters remain enabled in the DB
// so ResumeAll can restart them.
func (s *Service) PauseAll() int {
	n := s.manager.RunningCount()
	s.manager.StopAll()
	s.manager.Logs.Push(fmt.Sprintf("[sing-box] all processes paused (%d stopped)", n), "system")
	return n
}

// ResumeAll restarts all enabled adapters.
// It resets the auto-restart counter for each adapter so that the
// handleProcessExit loop will retry from scratch if the process exits
// unexpectedly after a manual resume.
func (s *Service) ResumeAll(ctx context.Context) (int, error) {
	if !singbox.IsInstalled() {
		return 0, fmt.Errorf("sing-box not installed")
	}
	db, err := s.ensureDB(ctx)
	if err != nil {
		return 0, err
	}
	adapters, err := db.List(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, a := range adapters {
		// --- SOCKS5 ---
		if a.Enabled && a.Socks5Configured && a.ListenPort > 0 {
			s.restartMu.Lock()
			delete(s.restartInfo, a.ID)
			s.restartMu.Unlock()

			if s.manager.IsRunning(a.ID) {
				if a.Status != "running" {
					_ = db.UpdateStatus(ctx, a.ID, "running", "")
				}
			} else {
				if startErr := s.startInstance(ctx, a); startErr != nil {
					_ = db.UpdateStatus(ctx, a.ID, "error", startErr.Error())
					s.logger.Warn("resume socks5 failed", "id", a.ID, "error", startErr)
				} else {
					_ = db.UpdateStatus(ctx, a.ID, "running", "")
					started++
				}
			}
		}

		// --- HTTP ---
		if a.HTTPEnabled && a.HTTPConfigured && a.HTTPListenPort > 0 {
			httpID := a.ID + "-http"
			s.restartMu.Lock()
			delete(s.restartInfo, httpID)
			s.restartMu.Unlock()

			if s.manager.IsRunning(httpID) {
				if a.HTTPStatus != "running" {
					_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")
				}
			} else {
				if startErr := s.startHTTPInstance(ctx, a); startErr != nil {
					_ = db.UpdateHTTPStatus(ctx, a.ID, "error", startErr.Error())
					s.logger.Warn("resume http failed", "id", a.ID, "error", startErr)
				} else {
					_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")
					started++
				}
			}
		}
	}
	s.manager.Logs.Push(fmt.Sprintf("[sing-box] resumed %d processes", started), "system")
	return started, nil
}

// CheckSingboxStatus returns the sing-box installation status.
func CheckSingboxStatus() singbox.Status {
	return singbox.CheckStatus()
}

// InstallSingbox downloads and installs sing-box.
func InstallSingbox(version string, progressFn func(string)) error {
	return singbox.Install(version, progressFn)
}

// UninstallSingbox removes the sing-box installation.
func UninstallSingbox() error {
	return singbox.Uninstall()
}

// ScanSingbox scans the install directory for a manually placed sing-box binary,
// verifies it works, and registers it.
func ScanSingbox() (singbox.Status, error) {
	return singbox.ScanLocal()
}

// EnvironmentCheck reports the readiness of database and sing-box.
func (s *Service) EnvironmentCheck(ctx context.Context) EnvStatus {
	status := EnvStatus{
		DBPath: s.dbPath,
	}
	_, err := os.Stat(s.dbPath)
	status.DBExists = err == nil
	if status.DBExists {
		db, openErr := s.ensureDB(ctx)
		if openErr != nil {
			status.DBReady = false
			status.DBError = openErr.Error()
		} else {
			status.DBReady = true
			_ = db
		}
	}
	// Check if sing-box is properly installed (marker + binary verified).
	sbStatus := singbox.CheckStatus()
	status.SingboxOK = sbStatus.Installed
	if sbStatus.Installed {
		status.SingboxMsg = sbStatus.Version
	} else if sbStatus.Error != "" {
		status.SingboxMsg = sbStatus.Error
	} else {
		status.SingboxMsg = "未安装"
	}
	return status
}

// StartEnabled starts all enabled node adapters on service initialization
// and launches the health-check goroutine.
// Each adapter is retried up to maxStartupRetries times before moving on.
func (s *Service) StartEnabled(ctx context.Context) {
	s.logger.Info("BUILD-TAG: edit-restart-fix-v1")
	// Skip if sing-box is not properly installed (marker + binary).
	if !singbox.IsInstalled() {
		s.logger.Info("sing-box not installed, skipping enabled adapters startup")
		return
	}
	db, err := s.ensureDB(ctx)
	if err != nil {
		s.logger.Warn("node adapter db not ready for startup", "error", err)
		return
	}
	adapters, err := db.List(ctx)
	if err != nil {
		s.logger.Warn("list node adapters for startup", "error", err)
		return
	}
	for _, a := range adapters {
		// --- SOCKS5 ---
		if a.Enabled && a.Socks5Configured && a.ListenPort > 0 {
			var startErr error
			for attempt := 1; attempt <= maxStartupRetries; attempt++ {
				startErr = s.startInstance(ctx, a)
				if startErr == nil {
					s.logger.Info("start socks5 adapter succeeded", "id", a.ID, "name", a.Name, "attempt", attempt)
					_ = db.UpdateStatus(ctx, a.ID, "running", "")
					break
				}
				s.logger.Warn("start socks5 adapter failed, retrying",
					"id", a.ID, "name", a.Name, "attempt", attempt, "max", maxStartupRetries, "error", startErr)
				time.Sleep(startupRetryDelay)
			}
			if startErr != nil {
				s.logger.Error("start socks5 adapter failed after all retries",
					"id", a.ID, "name", a.Name, "max_attempts", maxStartupRetries)
				_ = db.UpdateStatus(ctx, a.ID, "error", startErr.Error())
			}
		}

		// --- HTTP ---
		if a.HTTPEnabled && a.HTTPConfigured && a.HTTPListenPort > 0 {
			var startErr error
			for attempt := 1; attempt <= maxStartupRetries; attempt++ {
				startErr = s.startHTTPInstance(ctx, a)
				if startErr == nil {
					s.logger.Info("start http adapter succeeded", "id", a.ID, "name", a.Name, "attempt", attempt)
					_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")
					break
				}
				s.logger.Warn("start http adapter failed, retrying",
					"id", a.ID, "name", a.Name, "attempt", attempt, "max", maxStartupRetries, "error", startErr)
				time.Sleep(startupRetryDelay)
			}
			if startErr != nil {
				s.logger.Error("start http adapter failed after all retries",
					"id", a.ID, "name", a.Name, "max_attempts", maxStartupRetries)
				_ = db.UpdateHTTPStatus(ctx, a.ID, "error", startErr.Error())
			}
		}
	}

	// Start the health-check watchdog.
	go s.healthCheckLoop(ctx)
}
// startInstance builds the sing-box config and starts the process.
func (s *Service) startInstance(ctx context.Context, a NodeAdapter) error {
	cfg, err := singbox.BuildConfig(
		a.NodeConfigJSON, a.ListenHost, a.ListenPort,
		a.AuthEnabled, a.Username, a.Password,
	)
	if err != nil {
		return fmt.Errorf("build sing-box config: %w", err)
	}
	return s.manager.Start(ctx, a.ID, cfg)
}

// handleProcessExit is called by the Manager when a sing-box process exits.
// If the exit was unexpected (not user-initiated) and the adapter is still
// enabled in the database, it schedules an auto-restart.
func (s *Service) handleProcessExit(id string, err error, stopped bool) {
	// Determine if this is an HTTP process (id ends with "-http").
	isHTTP := strings.HasSuffix(id, "-http")
	baseID := id
	if isHTTP {
		baseID = strings.TrimSuffix(id, "-http")
	}

	// If the user explicitly stopped it, just update DB.
	if stopped {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if db, dbErr := s.ensureDB(ctx); dbErr == nil {
			if isHTTP {
				_ = db.UpdateHTTPStatus(ctx, baseID, "stopped", "")
			} else {
				_ = db.UpdateStatus(ctx, baseID, "stopped", "")
			}
		}
		s.restartMu.Lock()
		delete(s.restartInfo, id)
		s.restartMu.Unlock()
		return
	}

	// Unexpected exit — check if the adapter is still enabled.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, dbErr := s.ensureDB(ctx)
	if dbErr != nil {
		s.logger.Warn("handleProcessExit: cannot open db", "id", id, "error", dbErr)
		return
	}
	a, getErr := db.Get(ctx, baseID)
	if getErr != nil {
		// Adapter was deleted; nothing to do.
		return
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	// If a new process is already running (e.g. restarted by Update), sync status instead of marking error.
	if !isHTTP && s.manager.IsRunning(baseID) {
		_ = db.UpdateStatus(ctx, baseID, "running", "")
		s.restartMu.Lock()
		delete(s.restartInfo, id)
		s.restartMu.Unlock()
		return
	}
	if isHTTP && s.manager.IsRunning(id) {
		_ = db.UpdateHTTPStatus(ctx, baseID, "running", "")
		s.restartMu.Lock()
		delete(s.restartInfo, id)
		s.restartMu.Unlock()
		return
	}

	// Check if the relevant proxy type is still enabled.
	if isHTTP {
		_ = db.UpdateHTTPStatus(ctx, baseID, "error", errMsg)
		if !a.HTTPEnabled {
			return
		}
	} else {
		_ = db.UpdateStatus(ctx, baseID, "error", errMsg)
		if !a.Enabled {
			return
		}
	}

	// Check restart limits.
	s.restartMu.Lock()
	tracker, exists := s.restartInfo[id]
	if !exists {
		tracker = &restartTracker{}
		s.restartInfo[id] = tracker
	}
	tracker.count++
	attempt := tracker.count
	s.restartMu.Unlock()

	if attempt > maxAutoRestarts {
		s.logger.Warn("auto-restart limit reached, giving up",
			"id", id, "name", a.Name, "attempts", attempt)
		if isHTTP {
			_ = db.UpdateHTTPStatus(ctx, baseID, "error",
				fmt.Sprintf("auto-restart failed after %d attempts: %s", attempt, errMsg))
		} else {
			_ = db.UpdateStatus(ctx, baseID, "error",
				fmt.Sprintf("auto-restart failed after %d attempts: %s", attempt, errMsg))
		}
		return
	}

	// Exponential backoff: 5s, 10s, 20s, 40s, 80s (capped at 2min).
	delay := restartBackoffBase * time.Duration(1<<(attempt-1))
	if delay > restartBackoffMax {
		delay = restartBackoffMax
	}
	s.logger.Warn("sing-box exited unexpectedly, scheduling restart",
		"id", id, "name", a.Name, "attempt", attempt, "delay", delay, "error", errMsg)

	time.Sleep(delay)

	// Re-check that the adapter is still enabled (user may have disabled it during the wait).
	a, getErr = db.Get(ctx, baseID)
	if getErr != nil {
		return
	}
	if isHTTP && !a.HTTPEnabled {
		return
	}
	if !isHTTP && !a.Enabled {
		return
	}

	var startErr error
	if isHTTP {
		startErr = s.startHTTPInstance(ctx, a)
	} else {
		startErr = s.startInstance(ctx, a)
	}
	if startErr != nil {
		s.logger.Warn("auto-restart failed", "id", id, "error", startErr)
		if isHTTP {
			_ = db.UpdateHTTPStatus(ctx, baseID, "error", startErr.Error())
		} else {
			_ = db.UpdateStatus(ctx, baseID, "error", startErr.Error())
		}
		return
	}

	s.restartMu.Lock()
	tracker.lastRestart = time.Time{}
	s.restartMu.Unlock()

	if isHTTP {
		_ = db.UpdateHTTPStatus(ctx, baseID, "running", "")
	} else {
		_ = db.UpdateStatus(ctx, baseID, "running", "")
	}
	s.logger.Info("auto-restart succeeded", "id", id, "name", a.Name, "attempt", attempt)
}
// healthCheckLoop periodically verifies that every enabled adapter has a
// running sing-box process. If a process is missing, it triggers a restart.
// This acts as a safety net alongside the OnExit callback.
func (s *Service) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopWatch:
			return
		case <-ticker.C:
			s.healthCheck(ctx)
		}
	}
}

func (s *Service) healthCheck(ctx context.Context) {
	if !singbox.IsInstalled() {
		return
	}
	db, err := s.ensureDB(ctx)
	if err != nil {
		return
	}
	adapters, err := db.List(ctx)
	if err != nil {
		return
	}
	for _, a := range adapters {
		if !a.Enabled {
			continue
		}
		if s.manager.IsRunning(a.ID) {
			// Process alive — reset the restart counter.
			s.restartMu.Lock()
			if tracker, ok := s.restartInfo[a.ID]; ok {
				if time.Since(tracker.lastRestart) > restartBackoffReset {
					tracker.count = 0
				}
			}
			s.restartMu.Unlock()
			continue
		}

		// Process should be running but is not — attempt restart.
		s.logger.Warn("health check: process missing for enabled adapter, restarting",
			"id", a.ID, "name", a.Name, "listen",
			fmt.Sprintf("%s:%d", a.ListenHost, a.ListenPort))

		if startErr := s.startInstance(ctx, a); startErr != nil {
			s.logger.Warn("health check restart failed", "id", a.ID, "error", startErr)
			_ = db.UpdateStatus(ctx, a.ID, "error", startErr.Error())
		} else {
			_ = db.UpdateStatus(ctx, a.ID, "running", "")
			s.logger.Info("health check restart succeeded", "id", a.ID, "name", a.Name)
		}
	}
}

// Create saves a new node adapter to the database without starting a sing-box instance.
// The node is stored as "stopped". Use ConvertToSocks5 to create the local SOCKS5 proxy.
func (s *Service) Create(ctx context.Context, a NodeAdapter) (NodeAdapter, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return NodeAdapter{}, fmt.Errorf("database not ready: %w", err)
	}
	if a.ID == "" {
		a.ID = generateID()
	}
	if a.Socks5Configured {
		if a.ListenPort == 0 {
			port, err := s.nextPort(ctx, db)
			if err != nil {
				return NodeAdapter{}, fmt.Errorf("allocate port: %w", err)
			}
			a.ListenPort = port
		}
		if a.ListenHost == "" {
			a.ListenHost = "0.0.0.0"
		}
	} else {
		a.ListenPort = 0
		a.ListenHost = ""
	}
	if a.UDPIP == "" {
		a.UDPIP = s.globalUDPIP(ctx, db)
	}
	if a.HTTPConfigured {
		if a.HTTPListenPort == 0 {
			httpPort, err := s.nextHTTPPort(ctx, db)
			if err != nil {
				return NodeAdapter{}, fmt.Errorf("allocate http port: %w", err)
			}
			a.HTTPListenPort = httpPort
		}
		if a.HTTPListenHost == "" {
			a.HTTPListenHost = "0.0.0.0"
		}
	} else {
		a.HTTPListenPort = 0
		a.HTTPListenHost = ""
	}
	a.Status = "stopped"
	a.Enabled = false

	if err := db.Upsert(ctx, a); err != nil {
		s.logger.Error("Create: Upsert failed", "error", err, "name", a.Name)
		return NodeAdapter{}, err
	}
	s.logger.Info("Create: success", "id", a.ID, "name", a.Name, "listen_port", a.ListenPort)
	return a, nil
}

// ConvertToSocks5 starts the sing-box instance, creating a local SOCKS5 proxy
// that tunnels traffic through the node's outbound (e.g. Reality).
func (s *Service) ConvertToSocks5(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get adapter: %w", err)
	}
	if !a.Socks5Configured {
		return fmt.Errorf("socks5 not configured: please enable SOCKS5 local settings in edit dialog first")
	}

	s.logger.Info("converting to socks5",
		"id", a.ID, "name", a.Name,
		"protocol", a.Protocol,
		"listen", fmt.Sprintf("%s:%d", a.ListenHost, a.ListenPort),
		"config_len", len(a.NodeConfigJSON))

	a.Enabled = true
	if err := db.Upsert(ctx, a); err != nil {
		return fmt.Errorf("save adapter: %w", err)
	}
	if err := s.startInstance(ctx, a); err != nil {
		_ = db.UpdateStatus(ctx, a.ID, "error", err.Error())
		return fmt.Errorf("start instance: %w", err)
	}
	_ = db.UpdateStatus(ctx, a.ID, "running", "")

	// Verify the port is actually listening.
	addr := fmt.Sprintf("%s:%d", a.ListenHost, a.ListenPort)
	time.Sleep(200 * time.Millisecond)
	conn, probeErr := net.DialTimeout("tcp", addr, 2*time.Second)
	if probeErr != nil {
		s.logger.Warn("socks5 port not reachable after start", "id", a.ID, "addr", addr, "error", probeErr)
	} else {
		conn.Close()
		s.logger.Info("socks5 port verified reachable", "id", a.ID, "addr", addr)
	}

	return nil
}

// RemoveSocks5 stops the sing-box instance for the node adapter.
func (s *Service) RemoveSocks5(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return err
	}
	a.Enabled = false
	if err := db.Upsert(ctx, a); err != nil {
		return err
	}
	s.manager.Stop(id)
	return db.UpdateStatus(ctx, a.ID, "stopped", "")
}

// ConvertToHTTP starts the sing-box instance, creating a local HTTP proxy
// that tunnels traffic through the node's outbound (e.g. Reality).
// No authentication is enabled (transparent mode).
func (s *Service) ConvertToHTTP(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get adapter: %w", err)
	}

	if !a.HTTPConfigured {
		return fmt.Errorf("http not configured: please enable HTTP local settings in edit dialog first")
	}

	s.logger.Info("converting to http",
		"id", a.ID, "name", a.Name,
		"protocol", a.Protocol,
		"listen", fmt.Sprintf("%s:%d", a.HTTPListenHost, a.HTTPListenPort),
		"config_len", len(a.NodeConfigJSON))

	if a.HTTPListenPort == 0 {
		port, err := s.nextHTTPPort(ctx, db)
		if err != nil {
			return fmt.Errorf("allocate http port: %w", err)
		}
		a.HTTPListenPort = port
	}
	if a.HTTPListenHost == "" {
		a.HTTPListenHost = "0.0.0.0"
	}
	a.HTTPEnabled = true
	if err := db.Upsert(ctx, a); err != nil {
		return fmt.Errorf("save adapter: %w", err)
	}

	if err := s.startHTTPInstance(ctx, a); err != nil {
		_ = db.UpdateHTTPStatus(ctx, a.ID, "error", err.Error())
		return fmt.Errorf("start instance: %w", err)
	}
	_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")

	// Verify the port is actually listening.
	addr := fmt.Sprintf("%s:%d", a.HTTPListenHost, a.HTTPListenPort)
	time.Sleep(200 * time.Millisecond)
	conn, probeErr := net.DialTimeout("tcp", addr, 2*time.Second)
	if probeErr != nil {
		s.logger.Warn("http port not reachable after start", "id", a.ID, "addr", addr, "error", probeErr)
	} else {
		conn.Close()
		s.logger.Info("http port verified reachable", "id", a.ID, "addr", addr)
	}

	return nil
}

// RemoveHTTP stops the sing-box HTTP proxy instance for the node adapter.
func (s *Service) RemoveHTTP(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return err
	}
	a.HTTPEnabled = false
	if err := db.Upsert(ctx, a); err != nil {
		return err
	}
	s.manager.Stop(id + "-http")
	return db.UpdateHTTPStatus(ctx, a.ID, "stopped", "")
}

// startHTTPInstance builds the sing-box HTTP config and starts the process.
func (s *Service) startHTTPInstance(ctx context.Context, a NodeAdapter) error {
	cfg, err := singbox.BuildHTTPConfig(
		a.NodeConfigJSON, a.HTTPListenHost, a.HTTPListenPort,
	)
	if err != nil {
		return fmt.Errorf("build sing-box http config: %w", err)
	}
	return s.manager.Start(ctx, a.ID+"-http", cfg)
}

// CheckHTTP performs an HTTP probe against the node adapter's local HTTP endpoint.
func (s *Service) CheckHTTP(ctx context.Context, id string) (proxy.HTTPProbeResult, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return proxy.HTTPProbeResult{}, err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return proxy.HTTPProbeResult{}, err
	}
	addr := net.JoinHostPort(a.HTTPListenHost, strconv.Itoa(a.HTTPListenPort))
	result, probeErr := proxy.ProbeHTTP(ctx, addr, ProbeTimeout)
	resultJSON, _ := json.Marshal(result)
	_ = db.UpdateCheckResult(ctx, id, resultJSON)
	return result, probeErr
}

// Update updates an existing node adapter and restarts its instance.
func (s *Service) Update(ctx context.Context, id string, a NodeAdapter) (NodeAdapter, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return NodeAdapter{}, err
	}
	existing, err := db.Get(ctx, id)
	if err != nil {
		return NodeAdapter{}, err
	}
	a.ID = id
	a.CreatedAt = existing.CreatedAt
	if a.ListenPort == 0 {
		a.ListenPort = existing.ListenPort
	}
	if a.ListenHost == "" {
		a.ListenHost = existing.ListenHost
	}
	if a.UDPIP == "" {
		a.UDPIP = existing.UDPIP
	}
	if a.HTTPListenPort == 0 {
		a.HTTPListenPort = existing.HTTPListenPort
	}
	if a.HTTPListenHost == "" {
		a.HTTPListenHost = existing.HTTPListenHost
	}
	if !a.Socks5Configured {
		a.ListenPort = 0
		a.ListenHost = ""
		a.Enabled = false
	}
	if !a.HTTPConfigured {
		a.HTTPListenPort = 0
		a.HTTPListenHost = ""
		a.HTTPEnabled = false
	}
	// Preserve running state from existing, but only if still configured.
	if a.Socks5Configured {
		a.Enabled = existing.Enabled
	}
	if a.HTTPConfigured {
		a.HTTPEnabled = existing.HTTPEnabled
	}
	// Preserve status fields.
	if a.Status == "" {
		a.Status = existing.Status
	}
	if a.LastError == "" {
		a.LastError = existing.LastError
	}
	a.HTTPStatus = existing.HTTPStatus
	a.HTTPLastError = existing.HTTPLastError

	// Stop old SOCKS5 process if it was running (will restart below with new config).
	if existing.Enabled {
		s.manager.Stop(id)
	}
	// Stop old HTTP process if it was running.
	if existing.HTTPEnabled {
		s.manager.Stop(id + "-http")
	}

	if err := db.Upsert(ctx, a); err != nil {
		return NodeAdapter{}, err
	}

	// Restart SOCKS5 if enabled and properly configured.
	if a.Enabled && a.Socks5Configured && a.ListenPort > 0 {
		if err := s.startInstance(ctx, a); err != nil {
			_ = db.UpdateStatus(ctx, a.ID, "error", err.Error())
			a.Status = "error"
			a.LastError = err.Error()
			return a, nil
		}
		_ = db.UpdateStatus(ctx, a.ID, "running", "")
		a.Status = "running"
		a.LastError = ""
	}

	// Restart HTTP if enabled and properly configured.
	if a.HTTPEnabled && a.HTTPConfigured && a.HTTPListenPort > 0 {
		if err := s.startHTTPInstance(ctx, a); err != nil {
			_ = db.UpdateHTTPStatus(ctx, a.ID, "error", err.Error())
			a.HTTPStatus = "error"
			a.HTTPLastError = err.Error()
			return a, nil
		}
		_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")
		a.HTTPStatus = "running"
		a.HTTPLastError = ""
	}

	return a, nil
}
func (s *Service) Delete(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	s.manager.Stop(id)
	return db.Delete(ctx, id)
}

// Enable starts the sing-box instance for a node adapter.
func (s *Service) Enable(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return err
	}
	a.Enabled = true
	if err := db.Upsert(ctx, a); err != nil {
		return err
	}
	if err := s.startInstance(ctx, a); err != nil {
		_ = db.UpdateStatus(ctx, a.ID, "error", err.Error())
		return err
	}
	return db.UpdateStatus(ctx, a.ID, "running", "")
}

// Disable stops the sing-box instance for a node adapter.
func (s *Service) Disable(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return err
	}
	a.Enabled = false
	if err := db.Upsert(ctx, a); err != nil {
		return err
	}
	s.manager.Stop(id)
	return db.UpdateStatus(ctx, a.ID, "stopped", "")
}

// Get returns a single node adapter.
func (s *Service) Get(ctx context.Context, id string) (NodeAdapter, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return NodeAdapter{}, err
	}
	return db.Get(ctx, id)
}

// List returns all node adapters.
func (s *Service) List(ctx context.Context) ([]NodeAdapter, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		s.logger.Error("List: ensureDB failed", "error", err)
		return nil, err
	}
	result, listErr := db.List(ctx)
	if listErr != nil {
		s.logger.Error("List: db.List failed", "error", listErr)
	}
	s.logger.Info("List: returning adapters", "count", len(result))
	return result, listErr
}


// SyncStatus checks the actual running state of SOCKS5 and HTTP processes
// and corrects the database status to match reality.
func (s *Service) SyncStatus(ctx context.Context, id string) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return err
	}

	// Sync SOCKS5: only if enabled, configured, and has valid port
	if a.Enabled && a.Socks5Configured && a.ListenPort > 0 {
		if s.manager.IsRunning(a.ID) {
			if a.Status != "running" {
				_ = db.UpdateStatus(ctx, a.ID, "running", "")
			}
		}
	}

	// Sync HTTP: only if enabled, configured, and has valid port
	if a.HTTPEnabled && a.HTTPConfigured && a.HTTPListenPort > 0 {
		if s.manager.IsRunning(a.ID + "-http") {
			if a.HTTPStatus != "running" {
				_ = db.UpdateHTTPStatus(ctx, a.ID, "running", "")
			}
		}
	}

	return nil
}

// Check performs a SOCKS5 probe against the node adapter's local endpoint.
func (s *Service) Check(ctx context.Context, id string) (proxy.ProbeResult, error) {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return proxy.ProbeResult{}, err
	}
	a, err := db.Get(ctx, id)
	if err != nil {
		return proxy.ProbeResult{}, err
	}
	addr := net.JoinHostPort(a.ListenHost, strconv.Itoa(a.ListenPort))
	username, password := "", ""
	if a.AuthEnabled {
		username = a.Username
		password = a.Password
	}
	result, probeErr := proxy.ProbeSOCKS5(ctx, addr, username, password, ProbeTimeout)
	resultJSON, _ := json.Marshal(result)
	_ = db.UpdateCheckResult(ctx, id, resultJSON)
	return result, probeErr
}

// GlobalConfig returns the current global node adapter configuration.
func (s *Service) GlobalConfig(ctx context.Context) GlobalConfig {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return GlobalConfig{ListenIP: "0.0.0.0", UDPIP: "0.0.0.0"}
	}
	var raw string
	err = db.db.QueryRowContext(ctx,
		`SELECT value_json FROM kv WHERE key = ?`, globalConfigKey,
	).Scan(&raw)
	if err != nil {
		return GlobalConfig{ListenIP: "0.0.0.0", UDPIP: "0.0.0.0"}
	}
	var cfg GlobalConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return GlobalConfig{ListenIP: "0.0.0.0", UDPIP: "0.0.0.0"}
	}
	if cfg.ListenIP == "" {
		cfg.ListenIP = "0.0.0.0"
	}
	if cfg.UDPIP == "" {
		cfg.UDPIP = "0.0.0.0"
	}
	return cfg
}

// SaveGlobalConfig persists the global node adapter configuration.
func (s *Service) SaveGlobalConfig(ctx context.Context, cfg GlobalConfig) error {
	db, err := s.ensureDB(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = db.db.ExecContext(ctx, `
		INSERT INTO kv (key, value_json) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json
	`, globalConfigKey, string(raw))
	return err
}


func (s *Service) nextHTTPPort(ctx context.Context, db *DB) (int, error) {
	maxPort, err := db.MaxPort(ctx)
	if err != nil {
		return 0, err
	}
	start := DefaultHTTPStartPort
	if maxPort >= start {
		start = maxPort + 1
	}
	if start > 65535 {
		return 0, errors.New("no available port")
	}
	return start, nil
}

func (s *Service) nextPort(ctx context.Context, db *DB) (int, error) {
	maxPort, err := db.MaxPort(ctx)
	if err != nil {
		return 0, err
	}
	start := DefaultStartPort
	if maxPort >= start {
		start = maxPort + 1
	}
	if start > 65535 {
		return 0, errors.New("no available port")
	}
	return start, nil
}

func (s *Service) globalListenIP(ctx context.Context, db *DB) string {
	return s.GlobalConfig(ctx).ListenIP
}

func (s *Service) globalUDPIP(ctx context.Context, db *DB) string {
	return s.GlobalConfig(ctx).UDPIP
}

func generateID() string {
	b := make([]byte, 4)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
