package daemon_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/specscore/codegrapher/daemon"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current
// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected
func TestBuiltBinaryDaemonLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("built-binary daemon integration test")
	}
	repoRoot := repositoryRoot(t)
	testRoot := t.TempDir()
	binaryName := "codegrapher"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(testRoot, binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/codegrapher")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build codegrapher: %v\n%s", err, output)
	}

	projectPath := filepath.Join(testRoot, "repo")
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(projectPath, "main.go")
	if err := os.WriteFile(sourcePath, []byte("package sample\n\nfunc Before() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "-q", projectPath)
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("initialize Git fixture: %v\n%s", err, output)
	}
	stateDir := filepath.Join(testRoot, "state")
	runCLI(t, binaryPath, stateDir, "init", projectPath)
	started := runCLI(t, binaryPath, stateDir, "daemon", "start", projectPath)
	if !strings.Contains(started, "Owner: current user") || !strings.Contains(started, "Stop: codegrapher daemon stop") || !strings.Contains(started, "Browser link: https://codegrapher.dev/browse/") {
		t.Fatalf("start output omitted owner or teardown:\n%s", started)
	}
	t.Cleanup(func() {
		command := exec.Command(binaryPath, "daemon", "stop")
		command.Env = append(os.Environ(), "CODEGRAPH_DAEMON_DIR="+stateDir)
		_ = command.Run()
	})

	first := daemonStatus(t, binaryPath, stateDir)
	if first.Lifecycle != daemon.LifecycleReady || !first.Health.Live || !first.Health.WatchReady || !first.Health.IndexCurrent {
		t.Fatalf("initial status = %+v", first)
	}
	if first.BrowserEndpoint == "" {
		t.Fatal("ready daemon omitted browser endpoint")
	}
	browserSecret := browserSecretFromOutput(t, started)
	publicRequest, err := http.NewRequest(http.MethodGet, first.BrowserEndpoint+"/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	publicRequest.Header.Set("Authorization", "Bearer "+browserSecret)
	publicResponse, err := http.DefaultClient.Do(publicRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publicResponse.Body.Close() }()
	if publicResponse.StatusCode != http.StatusOK {
		t.Fatalf("authenticated browser status = %d", publicResponse.StatusCode)
	}
	controlWithBrowserToken, err := http.NewRequest(http.MethodGet, first.Endpoint+"/control/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	controlWithBrowserToken.Header.Set("Authorization", "Bearer "+browserSecret)
	controlWithBrowserToken.Header.Set("X-CodeGrapher-Nonce", "wrong")
	controlResponse, err := http.DefaultClient.Do(controlWithBrowserToken)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controlResponse.Body.Close() }()
	if controlResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("browser token authorized control API: %d", controlResponse.StatusCode)
	}
	duplicate := runCLI(t, binaryPath, stateDir, "daemon", "start", projectPath)
	if !strings.Contains(duplicate, fmt.Sprintf("PID: %d", first.PID)) {
		t.Fatalf("same-path start was not idempotent:\n%s", duplicate)
	}

	unauthenticated, err := http.Get(first.Endpoint + "/control/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthenticated.StatusCode, http.StatusUnauthorized)
	}

	if err := os.WriteFile(sourcePath, []byte("package sample\n\nfunc After() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterEdit := waitForNewGenerationCurrent(t, binaryPath, stateDir, first.Health.AcceptedGeneration, 8*time.Second)
	if !afterEdit.Health.IndexCurrent || afterEdit.Health.AcceptedGeneration == 0 || afterEdit.Health.AcceptedGeneration != afterEdit.Health.CompletedGeneration {
		t.Fatalf("post-edit status = %+v", afterEdit)
	}
	waitForNode(t, binaryPath, stateDir, projectPath, "After", 2*time.Second)

	runCLI(t, binaryPath, stateDir, "daemon", "restart")
	afterRestart := daemonStatus(t, binaryPath, stateDir)
	if afterRestart.Lifecycle != daemon.LifecycleReady || !afterRestart.Health.IndexCurrent {
		t.Fatalf("post-restart status = %+v", afterRestart)
	}
	runCLI(t, binaryPath, stateDir, "daemon", "stop")
	stopped := daemonStatus(t, binaryPath, stateDir)
	if stopped.Lifecycle != daemon.LifecycleStopped || stopped.Health.Live || stopped.PID != 0 {
		t.Fatalf("stopped status = %+v", stopped)
	}
	assertEndpointClosed(t, afterRestart.Endpoint)
	assertEndpointClosed(t, afterRestart.BrowserEndpoint)
}

func assertEndpointClosed(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid endpoint %q: %v", endpoint, err)
	}
	connection, err := net.DialTimeout("tcp", parsed.Host, 300*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatalf("listener remained reachable after joined stop: %s", endpoint)
	}
}

func browserSecretFromOutput(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "Browser link: ") {
			continue
		}
		parsed, err := url.Parse(strings.TrimPrefix(line, "Browser link: "))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(parsed.EscapedPath(), "/repos/") || !strings.Contains(parsed.EscapedPath(), "/revisions/") || !strings.HasSuffix(parsed.EscapedPath(), "/tree") {
			t.Fatalf("browser link is not a canonical repository route: %s", parsed.EscapedPath())
		}
		values, err := url.ParseQuery(parsed.Fragment)
		if err != nil {
			t.Fatal(err)
		}
		if secret := values.Get("secret"); secret != "" {
			return secret
		}
	}
	t.Fatal("browser link secret missing")
	return ""
}

func waitForNewGenerationCurrent(t *testing.T, binaryPath, stateDir string, previous uint64, timeout time.Duration) daemon.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var status daemon.Status
	for time.Now().Before(deadline) {
		status = daemonStatus(t, binaryPath, stateDir)
		if status.Health.IndexCurrent && status.Health.AcceptedGeneration > previous && status.Health.AcceptedGeneration == status.Health.CompletedGeneration {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon did not become current before timeout: %+v", status)
	return daemon.Status{}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Dir(filepath.Dir(file))
}

func runCLI(t *testing.T, binaryPath, stateDir string, args ...string) string {
	t.Helper()
	command := exec.Command(binaryPath, args...)
	command.Env = append(os.Environ(), "CODEGRAPH_DAEMON_DIR="+stateDir)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binaryPath, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func daemonStatus(t *testing.T, binaryPath, stateDir string) daemon.Status {
	t.Helper()
	output := runCLI(t, binaryPath, stateDir, "daemon", "status", "--format=json")
	var status daemon.Status
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("decode daemon status: %v\n%s", err, output)
	}
	return status
}

func waitForNode(t *testing.T, binaryPath, stateDir, projectPath, node string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		command := exec.Command(binaryPath, "node", node, "--path", projectPath, "--format=json")
		command.Env = append(os.Environ(), "CODEGRAPH_DAEMON_DIR="+stateDir)
		output, err := command.CombinedOutput()
		if err == nil && strings.Contains(string(output), `"name": "`+node+`"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node %q was not indexed before timeout", node)
}
