package certification

// Execution ownership is a durable fact, not a local flag: two processes over
// the same data directory and database must not both submit orders, so the
// second one has to fail before it reaches the venue.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nofx/trader/execution"
	"nofx/trading/automation"
	"nofx/trading/risk"
)

// runOwnershipHelper mirrors main.go: take the execution lock, then start the
// unattended runtime. Without the lock it must die instead of running.
func runOwnershipHelper(t *testing.T) {
	ctx := context.Background()
	db := openCertificationDB(t, os.Getenv(certHelperDB))
	release, err := execution.AcquireProcessLock(ctx, db, os.Getenv(certHelperLockDir))
	if err != nil {
		// The parent asserts on this line; errExecutionOwned is unexported.
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	defer func() { _ = release() }()

	risks, err := risk.NewStore(db)
	if err != nil {
		t.Fatalf("certification: ownership risk store: %v", err)
	}
	ctrl := certificationController(t, db, risks, nil, os.Getenv(certHelperAccount), venueBinance, time.Now)
	runtime, err := automation.NewRuntime(db, ctrl, risks,
		func(context.Context, string, string) (execution.ManagedTrader, execution.Binding, error) {
			return nil, execution.Binding{}, errors.New("certification: no exchange configuration in the ownership test")
		}, nil, time.Now)
	if err != nil {
		t.Fatalf("certification: ownership runtime: %v", err)
	}
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("certification: ownership runtime start: %v", err)
	}
	if err := os.WriteFile(os.Getenv(certHelperMarker), []byte("owned"), 0o600); err != nil {
		t.Fatalf("certification: ownership marker: %v", err)
	}
	// Hold ownership until the parent says the second process is done.
	for {
		if _, err := os.Stat(os.Getenv(certHelperRelease)); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	shutdown, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdown); err != nil {
		t.Fatalf("certification: ownership runtime shutdown: %v", err)
	}
}

// TestManagedSecondProcessRejected proves exactly one process owns unattended
// execution: the loser exits non-zero with the ownership error and never starts
// its runtime.
func TestManagedSecondProcessRejected(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("certification: data dir: %v", err)
	}
	dbPath := filepath.Join(dir, "ownership.db")
	ownerMarker := filepath.Join(dir, "owner.marker")
	loserMarker := filepath.Join(dir, "loser.marker")
	release := filepath.Join(dir, "owner.release")
	baseEnv := func(step, marker string) []string {
		return helperEnv(step,
			certHelperDB+"="+dbPath,
			certHelperLockDir+"="+dataDir,
			certHelperMarker+"="+marker,
			certHelperRelease+"="+release,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	owner := helperCommand(t, ctx, baseEnv(crashHelperOwnership, ownerMarker))
	var ownerOutput bytes.Buffer
	owner.Stdout, owner.Stderr = &ownerOutput, &ownerOutput
	if err := owner.Start(); err != nil {
		t.Fatalf("certification: start the owner: %v", err)
	}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- owner.Wait() }()
	waitForCertificationFile(t, ownerMarker, 30*time.Second)

	loser := helperCommand(t, ctx, baseEnv(crashHelperOwnership, loserMarker))
	var loserOutput bytes.Buffer
	loser.Stdout, loser.Stderr = &loserOutput, &loserOutput
	err := loser.Run()
	if err == nil {
		t.Fatalf("certification: a second process started unattended execution while one owner held the lock")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("certification: second process failed without a non-zero exit (%v)", err)
	}
	if !strings.Contains(loserOutput.String(), "another process owns unattended execution on this database") {
		t.Fatalf("certification: second process exited for the wrong reason: %s", certDescribe(&loserOutput))
	}
	if _, err := os.Stat(loserMarker); err == nil {
		t.Fatalf("certification: the rejected process still entered the signal loop")
	}

	if err := os.WriteFile(release, []byte("stop"), 0o600); err != nil {
		t.Fatalf("certification: release the owner: %v", err)
	}
	select {
	case err := <-ownerDone:
		if err != nil {
			t.Fatalf("certification: the owner failed after releasing the lock: %v\n%s", err, certDescribe(&ownerOutput))
		}
	case <-ctx.Done():
		t.Fatalf("certification: the owner never released the lock: %s", certDescribe(&ownerOutput))
	}
}
