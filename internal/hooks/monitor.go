// Package hooks provides lifecycle hook execution for the Mitto web server.

package hooks

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
)

const (
	monitorInitialDelay           = 1 * time.Minute
	monitorCheckInterval          = 30 * time.Second
	monitorPostRestartDelay       = 1 * time.Minute
	monitorPreRestartWait         = 30 * time.Second
	monitorRequestTimeout         = 10 * time.Second
	monitorMaxConsecutiveRestarts = 10
	monitorMaxCheckInterval       = 5 * time.Minute
	monitorFailureRetries         = 2               // Additional retries before restarting
	monitorRetryDelay             = 10 * time.Second // Delay between retries
)

// HealthMonitorConfig contains the configuration for a HealthMonitor.
type HealthMonitorConfig struct {
	Address   string
	UpHook    config.WebHook
	DownHook  config.WebHook
	Port      int
	OnFailure func(HookFailure)
	OnRestart func(attempt int)
	SetUpHook func(*Process) // Callback to update the up-hook reference in caller (e.g., ShutdownManager)
}

// HealthMonitor periodically checks an external URL and restarts lifecycle hooks
// if the address becomes unreachable.
type HealthMonitor struct {
	cfg          HealthMonitorConfig
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	restartCount int
}

// NewHealthMonitor creates a new health monitor.
func NewHealthMonitor(cfg HealthMonitorConfig) *HealthMonitor {
	return &HealthMonitor{
		cfg:  cfg,
		done: make(chan struct{}),
	}
}

// Start launches the monitoring goroutine.
func (m *HealthMonitor) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.run(ctx)
}

// Stop cancels the monitoring goroutine and waits for it to exit.
func (m *HealthMonitor) Stop() {
	if m == nil {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	<-m.done
}

func (m *HealthMonitor) run(ctx context.Context) {
	defer close(m.done)

	logger := logging.Hook()
	logger.Info("Health monitor started",
		"address", m.cfg.Address,
		"initial_delay", monitorInitialDelay,
		"check_interval", monitorCheckInterval,
	)

	// Wait initial delay before first check
	select {
	case <-ctx.Done():
		logger.Debug("Health monitor stopped during initial delay")
		return
	case <-time.After(monitorInitialDelay):
	}

	checkInterval := monitorCheckInterval
	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			logger.Debug("Health monitor stopped")
			return
		case <-time.After(checkInterval):
		}

		if m.checkHealth(ctx) {
			// Success — reset backoff
			if consecutiveFailures > 0 {
				logger.Info("External address recovered",
					"address", m.cfg.Address,
					"after_failures", consecutiveFailures,
				)
			}
			consecutiveFailures = 0
			checkInterval = monitorCheckInterval
			continue
		}

		// First check failed — retry a few times to confirm before restarting
		logger.Info("Health check failed, retrying to confirm",
			"address", m.cfg.Address,
			"retries", monitorFailureRetries,
			"retry_delay", monitorRetryDelay,
		)

		confirmed := true // assume failure is confirmed
		for retry := 0; retry < monitorFailureRetries; retry++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(monitorRetryDelay):
			}
			if m.checkHealth(ctx) {
				logger.Info("Health check recovered on retry",
					"address", m.cfg.Address,
					"retry", retry+1,
				)
				confirmed = false
				break
			}
			logger.Info("Health check retry failed",
				"address", m.cfg.Address,
				"retry", retry+1,
				"of", monitorFailureRetries,
			)
		}

		if !confirmed {
			// Recovered during retries — reset and continue
			consecutiveFailures = 0
			checkInterval = monitorCheckInterval
			continue
		}

		// Failure confirmed after all retries
		consecutiveFailures++
		m.mu.Lock()
		m.restartCount++
		attempt := m.restartCount
		m.mu.Unlock()

		logger.Warn("External address unreachable (confirmed after retries), restarting hooks",
			"address", m.cfg.Address,
			"attempt", attempt,
			"consecutive_failures", consecutiveFailures,
		)

		// Notify UI
		if m.cfg.OnRestart != nil {
			m.cfg.OnRestart(attempt)
		}

		// Restart hooks
		m.restartHooks(ctx)

		// Apply backoff if we've hit max consecutive restarts
		if consecutiveFailures >= monitorMaxConsecutiveRestarts {
			checkInterval = checkInterval * 2
			if checkInterval > monitorMaxCheckInterval {
				checkInterval = monitorMaxCheckInterval
			}
			logger.Warn("Max consecutive restarts reached, backing off",
				"new_interval", checkInterval,
				"consecutive_failures", consecutiveFailures,
			)
		}

		// Post-restart stabilization delay
		select {
		case <-ctx.Done():
			logger.Debug("Health monitor stopped during post-restart delay")
			return
		case <-time.After(monitorPostRestartDelay):
		}
	}
}


// checkHealth performs an HTTP GET to the external address.
// Returns true if the response status is 2xx or 3xx.
func (m *HealthMonitor) checkHealth(ctx context.Context) bool {
	logger := logging.Hook()

	client := &http.Client{
		Timeout: monitorRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects — a redirect response means the server is reachable
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.Address, nil)
	if err != nil {
		logger.Error("Failed to create health check request",
			"address", m.cfg.Address,
			"error", err,
		)
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("Health check failed",
			"address", m.cfg.Address,
			"error", err,
		)
		return false
	}
	defer resp.Body.Close()

	// 2xx and 3xx are considered healthy
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	if !healthy {
		logger.Debug("Health check unhealthy status",
			"address", m.cfg.Address,
			"status", resp.StatusCode,
		)
	}
	return healthy
}

// restartHooks stops the current up-hook, runs the down-hook, waits, then starts a new up-hook.
func (m *HealthMonitor) restartHooks(ctx context.Context) {
	logger := logging.Hook()

	// Step 1: Run the down hook
	logger.Info("Running down hook for restart",
		"command", m.cfg.DownHook.Command,
	)
	RunDown(m.cfg.DownHook, m.cfg.Port)

	// Step 2: Wait before restarting
	select {
	case <-ctx.Done():
		return
	case <-time.After(monitorPreRestartWait):
	}

	// Step 3: Start the up hook
	logger.Info("Running up hook for restart",
		"command", m.cfg.UpHook.Command,
	)
	var opts []StartUpOption
	if m.cfg.OnFailure != nil {
		opts = append(opts, WithOnFailure(m.cfg.OnFailure))
	}
	newProcess := StartUp(m.cfg.UpHook, m.cfg.Port, opts...)

	// Update the caller's reference to the up-hook process
	if m.cfg.SetUpHook != nil && newProcess != nil {
		m.cfg.SetUpHook(newProcess)
	}

	fmt.Printf("🔄 Hooks restarted (attempt %d)\n", m.restartCount)
	logger.Info("Hook restart complete",
		"attempt", m.restartCount,
	)
}
