package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/errors"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/process"
)

// defaultAsyncHookTimeout is the default per-hook timeout in seconds.
const defaultAsyncHookTimeout = 600

// slugifyHookName converts a hook name into a safe filename component:
// lower-case, with non-alphanumeric runs collapsed to '-'.
func slugifyHookName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "hook"
	}
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(name, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "hook"
	}
	return slug
}

// RunStopAsyncHook is the entry point for the `grove hooks stop-async` command.
// It reads stop input from stdin, loads the repo's grove.toml, and runs each
// [[hooks.on_stop]] command in parallel. Per-hook artifacts (pid lockfile,
// log, summary) are stored under StateDir()/hooks/sessions/<session_id>/on_stop.
// Any hook that exits non-zero (or times out) causes stop-async to exit 2 with
// aggregated stderr, so Claude Code's asyncRewake surfaces the failure to the
// agent. If all hooks exit 0, stop-async exits 0 and the session stays stopped.
func RunStopAsyncHook() {
	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stop-async: read stdin: %v\n", err)
		os.Exit(1)
	}

	var data StopInput
	if err := json.Unmarshal(inputData, &data); err != nil {
		fmt.Fprintf(os.Stderr, "stop-async: parse input: %v\n", err)
		os.Exit(1)
	}

	// Resolve working directory: session metadata → stop cwd → env PWD.
	workingDir := resolveAsyncWorkingDir(data)
	if workingDir == "" {
		// Nothing to do.
		os.Exit(0)
	}

	cfg, err := config.LoadFrom(workingDir)
	if err != nil {
		if errors.GetCode(err) == errors.ErrCodeConfigNotFound {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "stop-async: load grove config: %v\n", err)
		os.Exit(1)
	}

	var hooksConfig config.HooksConfig
	if err := cfg.UnmarshalExtension("hooks", &hooksConfig); err != nil {
		fmt.Fprintf(os.Stderr, "stop-async: unmarshal hooks config: %v\n", err)
		os.Exit(1)
	}

	if len(hooksConfig.OnStop) == 0 {
		os.Exit(0)
	}

	sessionID := data.SessionID
	if sessionID == "" {
		sessionID = "unknown"
	}
	stateDir := filepath.Join(paths.StateDir(), "hooks", "sessions", sessionID, "on_stop")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "stop-async: create state dir: %v\n", err)
		os.Exit(1)
	}

	exitCode := executeAsyncHooks(hooksConfig.OnStop, workingDir, stateDir)
	os.Exit(exitCode)
}

// resolveAsyncWorkingDir picks the best working directory for loading grove.toml.
func resolveAsyncWorkingDir(data StopInput) string {
	if data.SessionID != "" {
		metadataFile := filepath.Join(paths.StateDir(), "hooks", "sessions", data.SessionID, "metadata.json")
		if b, err := os.ReadFile(metadataFile); err == nil {
			var md struct {
				WorkingDirectory string `json:"working_directory"`
			}
			if err := json.Unmarshal(b, &md); err == nil && md.WorkingDirectory != "" {
				return md.WorkingDirectory
			}
		}
	}
	if data.Cwd != "" {
		return data.Cwd
	}
	if wd := os.Getenv("PWD"); wd != "" {
		return wd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// executeAsyncHooks runs all configured on_stop hooks in parallel and returns
// the aggregated exit code (2 if any hook exited 2, otherwise 0).
func executeAsyncHooks(cmds []config.HookCommand, workingDir, stateDir string) int {
	var (
		wg           sync.WaitGroup
		stderrMu     sync.Mutex
		blockingErrs []string
	)

	for _, hc := range cmds {
		hc := hc
		wg.Add(1)
		go func() {
			defer wg.Done()
			stderr, exited2 := runSingleAsyncHook(hc, workingDir, stateDir)
			if exited2 {
				stderrMu.Lock()
				if stderr == "" {
					stderr = fmt.Sprintf("hook %q exited 2", hc.Name)
				}
				blockingErrs = append(blockingErrs, stderr)
				stderrMu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(blockingErrs) > 0 {
		fmt.Fprint(os.Stderr, strings.Join(blockingErrs, "\n\n"))
		if !strings.HasSuffix(blockingErrs[len(blockingErrs)-1], "\n") {
			fmt.Fprintln(os.Stderr)
		}
		return 2
	}
	return 0
}

// runSingleAsyncHook executes one hook, writing its pid/log/summary artifacts.
// Returns (stderr, exited2): stderr is populated when the hook exited 2.
func runSingleAsyncHook(hc config.HookCommand, workingDir, stateDir string) (string, bool) {
	slug := slugifyHookName(hc.Name)
	pidPath := filepath.Join(stateDir, slug+".pid")
	logPath := filepath.Join(stateDir, slug+".log")
	summaryPath := filepath.Join(stateDir, slug+".summary")

	if hc.CancelPrevious {
		if b, err := os.ReadFile(pidPath); err == nil {
			if oldPid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && oldPid > 0 {
				if process.IsProcessAlive(oldPid) {
					_ = syscall.Kill(oldPid, syscall.SIGTERM)
					// Give the old process a brief moment to exit.
					for i := 0; i < 20; i++ {
						if !process.IsProcessAlive(oldPid) {
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
				}
			}
		}
	}

	// marker-file gating: a `grove hooks disable` marker wins over env-var
	// checks so operators can toggle hooks while a Claude session is live
	// (env vars are captured at Claude Code startup and can't be changed).
	if IsHookDisabledByMarker(workingDir, hc.Name) {
		appendSummary(summaryPath, "skipped")
		return "", false
	}

	// env-var gating: explicit disable wins, then opt-in via enable_env.
	if hc.DisableEnv != "" && os.Getenv(hc.DisableEnv) != "" {
		appendSummary(summaryPath, "skipped")
		return "", false
	}
	if hc.EnableEnv != "" && os.Getenv(hc.EnableEnv) == "" {
		appendSummary(summaryPath, "skipped")
		return "", false
	}

	// run_if gating
	if hc.RunIf == "changes" {
		hasChanges, err := hasGitChanges(workingDir)
		if err != nil || !hasChanges {
			appendSummary(summaryPath, "skipped")
			return "", false
		}
	}

	// Write our PID to the lockfile; remove on exit.
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil { //nolint:gosec // pid file
		return "", false
	}
	defer os.Remove(pidPath)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", false
	}
	defer logFile.Close()

	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = defaultAsyncHookTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", hc.Command) //nolint:gosec // command from trusted config
	cmd.Dir = workingDir

	// Capture combined stdout+stderr to the log file AND a stderr-only buffer
	// so we can surface exit-2 errors back to Claude Code for rewake.
	var stderrBuf strings.Builder
	cmd.Stdout = logFile
	cmd.Stderr = io.MultiWriter(logFile, &stderrBuf)

	if err := cmd.Start(); err != nil {
		appendSummary(summaryPath, "failed")
		return "", false
	}
	// Overwrite the lockfile with the child's PID so cancel_previous targets
	// the actual hook process rather than the short-lived grove-hooks parent.
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644) //nolint:gosec // pid file

	runErr := cmd.Wait()

	status := "passed"
	blocking := false
	exitCode := 0
	if ctx.Err() == context.DeadlineExceeded {
		status = "killed"
		blocking = true
		exitCode = -1
	} else if runErr != nil {
		status = "failed"
		blocking = true
		if exitError, ok := runErr.(*exec.ExitError); ok {
			if ws, ok := exitError.Sys().(syscall.WaitStatus); ok {
				exitCode = ws.ExitStatus()
			}
		}
	}

	appendSummary(summaryPath, status)

	if blocking {
		msg := strings.TrimSpace(stderrBuf.String())
		header := fmt.Sprintf("hook %q failed", hc.Name)
		if status == "killed" {
			header = fmt.Sprintf("hook %q timed out after %ds", hc.Name, timeout)
		} else if exitCode != 0 {
			header = fmt.Sprintf("hook %q exited %d", hc.Name, exitCode)
		}
		if msg == "" {
			msg = header
		} else {
			msg = header + ":\n" + msg
		}
		return msg, true
	}
	return "", false
}

// appendSummary adds a timestamped line to the hook's summary file.
func appendSummary(summaryPath, status string) {
	f, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), status)
}
