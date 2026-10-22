package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type instance struct {
	cmd        *exec.Cmd
	configPath string
	cancel     context.CancelFunc
	stopped    bool // true = user explicitly stopped
}

// Manager manages the lifecycle of sing-box subprocesses.
type Manager struct {
	mu        sync.Mutex
	logger    *slog.Logger
	instances map[string]*instance
	configDir string
	// Logs captures sing-box stdout/stderr output.
	Logs *LogBuffer
	// OnExit is called in a goroutine when a process exits.
	// Parameters: id, error (nil for normal exit), stopped (true if user requested stop).
	OnExit func(id string, err error, stopped bool)
}

func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	configDir := filepath.Join(os.TempDir(), "vonet-singbox")
	_ = os.MkdirAll(configDir, 0o755)
	return &Manager{
		logger:    logger,
		instances: make(map[string]*instance),
		configDir: configDir,
		Logs:      NewLogBuffer(),
	}
}

// Start writes the config to a file and starts sing-box as a subprocess.
func (m *Manager) Start(ctx context.Context, id string, configJSON json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopLocked(id)

	m.Logs.Push(fmt.Sprintf("[sing-box] starting instance id=%s", id), "system")
	m.logger.Info("sing-box starting", "id", id, "config_len", len(configJSON))

	// Check binary
	binPath := BinaryPath
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("sing-box not installed at %s: %w", binPath, err)
	}

	// Write config file
	configPath := filepath.Join(m.configDir, fmt.Sprintf("%s.json", id))
	if err := os.WriteFile(configPath, configJSON, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Validate config
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer checkCancel()
	checkCmd := exec.CommandContext(checkCtx, binPath, "check", "-c", configPath)
	if output, err := checkCmd.CombinedOutput(); err != nil {
		_ = os.Remove(configPath)
		errMsg := string(output)
		m.Logs.Push(fmt.Sprintf("[sing-box] config check failed: %s", errMsg), "error")
		return fmt.Errorf("config validation failed: %s: %w", errMsg, err)
	}

	// Start sing-box — use context.Background() so the process lives beyond the HTTP request.
	instanceCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(instanceCtx, binPath, "run", "-c", configPath)

	// Capture stdout/stderr through pipes into the log buffer.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = os.Remove(configPath)
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = os.Remove(configPath)
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.Remove(configPath)
		m.Logs.Push(fmt.Sprintf("[sing-box] start failed: %v", err), "error")
		return fmt.Errorf("start process: %w", err)
	}

	// Capture instance pointer so the goroutine can read its stopped flag
	// even after the instance is replaced in the map by a subsequent Start call.
	curInst := &instance{
		cmd:        cmd,
		configPath: configPath,
		cancel:     cancel,
		stopped:    false,
	}
	m.instances[id] = curInst

	// Pipe process output to the log buffer (runs until process exits).
	go m.Logs.PipeLines(stdoutPipe, "stdout")
	go m.Logs.PipeLines(stderrPipe, "stderr")

	m.Logs.Push(fmt.Sprintf("[sing-box] process started id=%s pid=%d", id, cmd.Process.Pid), "system")
	m.logger.Info("sing-box process started", "id", id, "pid", cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		// Read stopped from the captured instance pointer. stopLocked sets
		// curInst.stopped=true before killing, so this correctly reflects
		// whether the exit was user-initiated.
		wasStopped := curInst.stopped

		m.mu.Lock()
		if current, exists := m.instances[id]; exists && current == curInst {
			_ = os.Remove(configPath)
			delete(m.instances, id)
		}
		m.mu.Unlock()

		if err != nil {
			m.Logs.Push(fmt.Sprintf("[sing-box] process exited id=%s error=%v stopped=%v", id, err, wasStopped), "system")
			m.logger.Warn("sing-box process exited", "id", id, "error", err, "stopped", wasStopped)
		} else {
			m.Logs.Push(fmt.Sprintf("[sing-box] process exited normally id=%s stopped=%v", id, wasStopped), "system")
			m.logger.Info("sing-box process exited normally", "id", id, "stopped", wasStopped)
		}

		// Notify the exit handler (if set).
		if m.OnExit != nil {
			m.OnExit(id, err, wasStopped)
		}
	}()

	return nil
}

// Stop explicitly stops a sing-box process. This marks it as "stopped" so the
// exit handler knows not to auto-restart.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked(id)
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.instances {
		m.stopLocked(id)
	}
}

func (m *Manager) stopLocked(id string) {
	inst, ok := m.instances[id]
	if !ok {
		return
	}
	inst.stopped = true
	inst.cancel()
	if inst.cmd.Process != nil {
		_ = inst.cmd.Process.Kill()
	}
	_ = os.Remove(inst.configPath)
	delete(m.instances, id)
	m.Logs.Push(fmt.Sprintf("[sing-box] process stopped id=%s", id), "system")
	m.logger.Info("sing-box process stopped", "id", id)
}

// IsRunning checks whether the given instance is alive.
func (m *Manager) IsRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.instances[id]
	return ok
}

// RunningCount returns the number of running sing-box instances.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances)
}
