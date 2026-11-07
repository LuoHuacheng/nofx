package certification

// Crash recovery is proven against state the kernel persisted: the test binary
// re-executes itself, the helper is killed with SIGKILL at one named breakpoint
// on the real request path, and the parent recovers over the same database and
// the same fake venue. Nothing is simulated in-process, and no production code
// carries a test switch.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/planning/live"
	"nofx/trader/binance"
	"nofx/trader/execution"
	"nofx/trader/okx"
	"nofx/trading/risk"
)

// certHelperAccount carries the managed account key the fixture bound, so the
// child talks about the same run and ledger rows as the parent.
const certHelperAccount = "CERT_HELPER_ACCOUNT"

// The parent hands the helper a whitelisted environment; these are the names.
const (
	certHelperStep    = "CERT_HELPER_STEP"
	certHelperBase    = "CERT_HELPER_BASE"
	certHelperDB      = "CERT_HELPER_DB"
	certHelperLockDir = "CERT_HELPER_LOCKDIR"
	certHelperVenue   = "CERT_HELPER_VENUE"
	certHelperNow     = "CERT_HELPER_NOW"
	certHelperMarker  = "CERT_HELPER_MARKER"
	certHelperRelease = "CERT_HELPER_RELEASE"
)

// Breakpoints are named after what the crash leaves on disk.
const (
	crashBeforeIntentCommit     = "before_intent_commit"
	crashAfterIntentCommit      = "after_intent_commit"
	crashAfterEntryAccepted     = "after_entry_accepted"
	crashAfterEntryFillBeforeSL = "after_entry_fill_before_sl"
	crashAfterSLAccepted        = "after_sl_accepted"
	crashAfterExitAccepted      = "after_exit_accepted"

	crashHelperOwnership    = "ownership"
	crashHelperFundingCurse = "funding_cursor"
)

var crashBreakpoints = []string{
	crashBeforeIntentCommit,
	crashAfterIntentCommit,
	crashAfterEntryAccepted,
	crashAfterEntryFillBeforeSL,
	crashAfterSLAccepted,
	crashAfterExitAccepted,
}

// Request classes the crash transport recognises on the venue wire.
const (
	certWriteNone       = ""
	certWriteLeverage   = "leverage"
	certWriteEntry      = "entry"
	certWriteProtection = "protection"
	certWriteExit       = "exit"
)

// crashProcess is a hard stop with no unwinding, which is the only way to prove
// recovery from state the OS really persisted.
func crashProcess() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}

// crashTransport kills the process at one breakpoint. Every breakpoint is a
// request the production code sends, so the crash lands mid-protocol: either
// before the venue sees the intent or after it booked it but before the process
// recorded the receipt.
type crashTransport struct {
	base http.RoundTripper
	step string
}

func (t *crashTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	kind := venueWriteKind(req)
	switch t.step {
	case crashBeforeIntentCommit:
		if kind != certWriteNone {
			crashProcess()
		}
	case crashAfterIntentCommit:
		if kind == certWriteEntry {
			crashProcess()
		}
	case crashAfterEntryAccepted:
		if kind == certWriteEntry {
			return forwardThenCrash(t.base, req)
		}
	case crashAfterEntryFillBeforeSL:
		if kind == certWriteProtection {
			crashProcess()
		}
	case crashAfterSLAccepted:
		if kind == certWriteProtection {
			return forwardThenCrash(t.base, req)
		}
	case crashAfterExitAccepted:
		if kind == certWriteExit {
			return forwardThenCrash(t.base, req)
		}
	}
	return t.base.RoundTrip(req)
}

// forwardThenCrash lets the venue book the order and then removes the process,
// which is exactly the "accepted, acknowledgement lost" failure mode.
func forwardThenCrash(base http.RoundTripper, req *http.Request) (*http.Response, error) {
	_, _ = base.RoundTrip(req)
	crashProcess()
	return nil, errors.New("certification: unreachable after a crash")
}

// venueWriteKind classifies a write request without touching the body the
// adapter signed.
func venueWriteKind(req *http.Request) string {
	if req.Method != http.MethodPost {
		return certWriteNone
	}
	body := peekBody(req)
	switch req.URL.Path {
	case "/fapi/v1/leverage", "/api/v5/account/set-leverage":
		return certWriteLeverage
	case "/fapi/v1/order", "/api/v5/trade/order":
		if reduceOnlyIntent(body) {
			return certWriteExit
		}
		return certWriteEntry
	case "/fapi/v1/algoOrder", "/api/v5/trade/order-algo":
		return certWriteProtection
	}
	return certWriteNone
}

// peekBody reads and restores the request body.
func peekBody(req *http.Request) []byte {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

// reduceOnlyIntent reports a reduce-only or market order, the two shapes both
// adapters use for an exit.
func reduceOnlyIntent(body []byte) bool {
	if form, err := url.ParseQuery(string(body)); err == nil {
		if strings.EqualFold(form.Get("reduceOnly"), "true") {
			return true
		}
		if strings.EqualFold(form.Get("type"), "market") || strings.EqualFold(form.Get("ordType"), "market") {
			return true
		}
	}
	var payload struct {
		ReduceOnly bool   `json:"reduceOnly"`
		Type       string `json:"type"`
		OrdType    string `json:"ordType"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	return payload.ReduceOnly || strings.EqualFold(payload.Type, "market") || strings.EqualFold(payload.OrdType, "market")
}

// helperEnv whitelists the environment: the child gets the OS basics the Go
// runtime needs plus the certification variables, and never inherits the
// parent's environment (no credential can leak into a second process).
func helperEnv(step string, extra ...string) []string {
	env := []string{}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(append(env, certHelperStep+"="+step), extra...)
}

// helperCommand starts this test binary as a child process.
func helperCommand(t *testing.T, ctx context.Context, env []string) *exec.Cmd {
	t.Helper()
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("certification: resolve test binary: %v", err)
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestManagedCrashHelperProcess$")
	cmd.Dir = t.TempDir()
	cmd.Env = env
	return cmd
}

// runUntilKilled runs the helper and requires that it died by SIGKILL at its
// breakpoint rather than exiting, skipping or hanging.
func runUntilKilled(t *testing.T, step string, env []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := helperCommand(t, ctx, env)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("certification: helper for %s never reached its breakpoint: %s", step, output.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("certification: helper for %s exited normally (%v) instead of dying: %s", step, err, output.String())
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("certification: helper for %s exited without SIGKILL (%v): %s", step, exitErr, output.String())
	}
}

// TestManagedCrashHelperProcess is the child entry point. Without the step
// variable it does nothing: the parent always drives it explicitly.
func TestManagedCrashHelperProcess(t *testing.T) {
	step := os.Getenv(certHelperStep)
	if step == "" {
		return
	}
	switch step {
	case crashHelperOwnership:
		runOwnershipHelper(t)
	case crashHelperFundingCurse:
		runFundingCursorHelper(t)
	default:
		runCrashHelper(t, step)
	}
}

// runCrashHelper drives the plan in a fresh process until the transport kills
// it at the named breakpoint.
func runCrashHelper(t *testing.T, step string) {
	venue := os.Getenv(certHelperVenue)
	base := os.Getenv(certHelperBase)
	dbPath := os.Getenv(certHelperDB)
	clock, err := time.Parse(time.RFC3339Nano, os.Getenv(certHelperNow))
	if err != nil {
		t.Fatalf("certification: helper clock: %v", err)
	}
	ctx := context.Background()
	db := openCertificationDB(t, dbPath)
	risks, err := risk.NewStore(db)
	if err != nil {
		t.Fatalf("certification: helper risk store: %v", err)
	}
	client := &http.Client{Transport: &crashTransport{base: http.DefaultTransport, step: step}}
	trader := managedTrader(t, ctx, venue, base, client)
	ctrl := certificationController(t, db, risks, trader, os.Getenv(certHelperAccount), venue, func() time.Time { return clock })

	views, err := ctrl.ListContext(ctx, "alice")
	if err != nil {
		t.Fatalf("certification: helper could not list runs: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("certification: helper found %d runs, want 1", len(views))
	}
	view := views[0]
	if view.State == "approved" {
		if _, err := ctrl.Execute(ctx, "alice", view.ID, view.ApprovalHash); err != nil {
			t.Fatalf("certification: helper could not execute the approved plan: %v", err)
		}
	}
	for i := 0; i < 8; i++ {
		got, err := ctrl.Get("alice", view.ID)
		if err == nil && got.State == "position_open" {
			break
		}
		ctrl.StepContext(ctx, "alice", view.ID)
	}
	if step == crashAfterExitAccepted {
		if _, err := ctrl.Flatten(ctx, "alice", view.ID, "certification crash helper"); err != nil {
			t.Fatalf("certification: helper could not flatten: %v", err)
		}
	}
	t.Fatalf("certification: helper never reached %q", step)
}

func openCertificationDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("certification: open %s: %v", path, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("certification: db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func managedTrader(t *testing.T, ctx context.Context, venue, base string, client *http.Client) execution.ManagedTrader {
	t.Helper()
	switch venue {
	case venueBinance:
		trader, err := binance.NewManagedAccountTraderWithEndpoint(ctx, "test-key", "test-secret", "",
			execution.EnvironmentSandbox, &binance.ManagedEndpoint{BaseURL: base, Client: client})
		if err != nil {
			t.Fatalf("certification: binance adapter: %v", err)
		}
		return trader
	case venueOKX:
		trader, err := okx.NewManagedAccountTraderWithEndpoint(ctx, "test-key", "test-secret", "pass",
			execution.EnvironmentSandbox, &okx.ManagedEndpoint{BaseURL: base, Client: client})
		if err != nil {
			t.Fatalf("certification: okx adapter: %v", err)
		}
		return trader
	}
	t.Fatalf("certification: unknown venue %q", venue)
	return nil
}

// certificationController wires a controller with no worker, exactly as the
// fixture does, so the caller drives one cycle at a time.
func certificationController(t *testing.T, db *gorm.DB, risks *risk.Store, trader execution.ManagedTrader, account, venue string, now func() time.Time) *live.Controller {
	t.Helper()
	factory := func(context.Context, string, string) (live.Broker, string, error) {
		return live.NOFXBroker{Trader: trader}, account, nil
	}
	ctrl, err := live.New(db, factory, &fixtureMarket{base: fixtureSnapshot(venue)})
	if err != nil {
		t.Fatalf("certification: controller: %v", err)
	}
	ctrl.Now = now
	ctrl.Interval = -1
	ctrl.Risk = risks
	return ctrl
}

// TestManagedCrashRecovery kills the process at each breakpoint and recovers in
// a new process over the same database and the same fake venue.
func TestManagedCrashRecovery(t *testing.T) {
	for _, venue := range []string{venueBinance, venueOKX} {
		for _, step := range crashBreakpoints {
			step := step
			t.Run(venue+"/"+step, func(t *testing.T) {
				f := newExchangeFixture(t, venue)
				approveCertificationPlan(t, f)
				runUntilKilled(t, step, helperEnv(step,
					certHelperBase+"="+f.fake.url(),
					certHelperDB+"="+f.dbPath,
					certHelperVenue+"="+f.venue,
					certHelperAccount+"="+f.binding.ID,
					certHelperNow+"="+f.clock.Format(time.RFC3339Nano),
					certHelperLockDir+"="+t.TempDir(),
				))
				// Capture what the dead process left before the restart rewrites
				// the run state.
				crashed := f.run()
				f.Restart()
				checkCrashRecovery(t, f, crashed, step)
			})
		}
	}
}

// approveCertificationPlan leaves an approved run for the helper to start, so
// the crash lands inside execution instead of before it.
func approveCertificationPlan(t *testing.T, f *exchangeFixture) {
	t.Helper()
	view, err := f.ctrl.Approve(context.Background(), "alice", f.plan.ID, f.plan.Hash, "APPROVE LIVE")
	if err != nil {
		t.Fatalf("certification: approve plan: %v", err)
	}
	f.approved = view
}

func certEntryPosts(f *exchangeFixture) int {
	entry, _, _, _ := f.fake.countsNow()
	return entry
}

func certProtectionPosts(f *exchangeFixture) int {
	_, protect, _, _ := f.fake.countsNow()
	return protect
}

func certExitPosts(f *exchangeFixture) int {
	_, _, exit, _ := f.fake.countsNow()
	return exit
}

// certVenueRole counts the orders the venue owns for one role.
func certVenueRole(f *exchangeFixture, role string) int {
	return len(f.fake.byRole(role))
}

// certAssertHoldKept requires the reservation to still be held while a position
// exists. Recovery may leave it reserved rather than exposed, which is the more
// conservative state and must never be mistaken for released.
func certAssertHoldKept(t *testing.T, f *exchangeFixture, when string) {
	t.Helper()
	if state := f.ReservationState(); state != "reserved" && state != "exposed" {
		t.Fatalf("certification: reservation is %q %s, want it still held", state, when)
	}
}

func certReservationRows(t *testing.T, f *exchangeFixture) int64 {
	t.Helper()
	var rows int64
	if err := f.db.Model(&risk.ReservationRecord{}).Where("run_id = ?", f.approved.ID).Count(&rows).Error; err != nil {
		t.Fatalf("certification: count reservations: %v", err)
	}
	return rows
}

// checkCrashRecovery asserts the durable facts each breakpoint leaves behind,
// then drives the recovered run to its terminal state.
func checkCrashRecovery(t *testing.T, f *exchangeFixture, crashed live.View, step string) {
	t.Helper()
	switch step {
	case crashBeforeIntentCommit:
		if crashed.State != "approved" || crashed.StartedAt != nil {
			t.Fatalf("certification: crash before the intent left run %q (started %v), want an untouched approval", crashed.State, crashed.StartedAt)
		}
		if entry := certEntryPosts(f); entry != 0 {
			t.Fatalf("certification: crash before the intent posted %d entries", entry)
		}
		if rows := certReservationRows(t, f); rows != 0 {
			t.Fatalf("certification: crash before the intent left %d risk reservations, want none", rows)
		}
		// A restart never replays an approval on its own.
		f.Steps(2)
		view := f.run()
		if view.State != "approved" || certEntryPosts(f) != 0 {
			t.Fatalf("certification: restart replayed an approved plan: state %q, %d entries", view.State, certEntryPosts(f))
		}
		if _, err := f.ctrl.Execute(context.Background(), "alice", view.ID, view.ApprovalHash); err != nil {
			t.Fatalf("certification: recovered execute: %v", err)
		}
		f.Steps(8)
		f.AssertOneEntry(t, "signal-1")
		certAssertHoldKept(t, f, "after the recovered entry")
		if err := f.Flatten(); err != nil {
			t.Fatalf("certification: recovered flatten: %v", err)
		}
		f.AssertFlatAndNoRiskOrders(t)
		f.AssertNoOpenOrders(t)
		if state := f.ReservationState(); state != "released" {
			t.Fatalf("certification: reservation is %q after a closed trade, want released", state)
		}

	case crashAfterIntentCommit:
		if crashed.State != "running" || crashed.StartedAt == nil {
			t.Fatalf("certification: intent commit left run %q (started %v), want running", crashed.State, crashed.StartedAt)
		}
		if len(crashed.Orders) != 1 || crashed.Orders[0].Status != "submitting" || crashed.Orders[0].Role != "entry" {
			t.Fatalf("certification: intent commit left orders %+v, want one submitting entry", crashed.Orders)
		}
		if entry := certEntryPosts(f); entry != 0 {
			t.Fatalf("certification: crash before the entry POST still posted %d entries", entry)
		}
		if state := f.ReservationState(); state != "reserved" {
			t.Fatalf("certification: reservation is %q after the intent commit, want reserved", state)
		}
		f.Steps(3)
		if view := f.run(); view.State != "needs_attention" {
			t.Fatalf("certification: recovered run is %q, want needs_attention while the entry is unconfirmed", view.State)
		}
		if entry := certEntryPosts(f); entry != 0 {
			t.Fatalf("certification: recovery resubmitted an unconfirmed entry (%d posts)", entry)
		}
		f.AssertAtMostOneEntry(t, "signal-1")
		if state := f.ReservationState(); state == "released" {
			t.Fatalf("certification: released the hold of an entry the venue never acknowledged")
		}

	case crashAfterEntryAccepted:
		if crashed.State != "running" {
			t.Fatalf("certification: crash after the entry acknowledgement left run %q, want running", crashed.State)
		}
		if entry := certEntryPosts(f); entry != 1 {
			t.Fatalf("certification: venue saw %d entries, want exactly 1", entry)
		}
		if protect := certProtectionPosts(f); protect != 0 {
			t.Fatalf("certification: a lost acknowledgement already produced %d protections", protect)
		}
		if state := f.ReservationState(); state != "reserved" {
			t.Fatalf("certification: reservation is %q after an unrecorded fill, want reserved", state)
		}
		f.Steps(8)
		view := f.run()
		if view.State != "position_open" {
			t.Fatalf("certification: recovered run is %q with a filled entry, want position_open", view.State)
		}
		position := f.fake.positionNow()
		stops := 0
		for _, order := range f.fake.byRole("sl") {
			if order.live() && order.quantity.Cmp(position) == 0 {
				stops++
			}
		}
		if stops != 1 {
			t.Fatalf("certification: filled position %s has %d matching stops, want 1", dec(position), stops)
		}
		certAssertHoldKept(t, f, "after the recovered fill")
		f.AssertOneEntry(t, "signal-1")
		if err := f.Flatten(); err != nil {
			t.Fatalf("certification: recovered flatten: %v", err)
		}
		f.AssertFlatAndNoRiskOrders(t)
		f.AssertNoOpenOrders(t)
		if view = f.run(); view.State != "completed" {
			t.Fatalf("certification: recovered run is %q after the exit, want completed", view.State)
		}
		if state := f.ReservationState(); state != "released" {
			t.Fatalf("certification: reservation is %q after the trade closed, want released", state)
		}

	case crashAfterEntryFillBeforeSL:
		if entry := certEntryPosts(f); entry != 1 {
			t.Fatalf("certification: venue saw %d entries, want exactly 1", entry)
		}
		if stops := certVenueRole(f, "sl"); stops != 0 {
			t.Fatalf("certification: the crash happened before the stop arrived, yet the venue owns %d stops", stops)
		}
		f.Steps(8)
		view := f.run()
		if view.ProtectionState != "exit_required" {
			t.Fatalf("certification: unprotected fill recovered to protection state %q, want exit_required", view.ProtectionState)
		}
		if view.State != "needs_attention" {
			t.Fatalf("certification: unprotected fill left run %q, want needs_attention", view.State)
		}
		if stops := certVenueRole(f, "sl"); stops != 0 {
			t.Fatalf("certification: resubmitted a stop whose submission was never acknowledged (%d venue stops)", stops)
		}
		if exit := certExitPosts(f); exit != 0 {
			t.Fatalf("certification: exited on its own without an operator decision (%d exits)", exit)
		}
		if state := f.ReservationState(); state == "released" {
			t.Fatalf("certification: released the hold of an unprotected fill")
		}
		// The operator's exit still reaches the venue, but the unconfirmed stop
		// keeps the run halted: that is what blocks the next entry.
		if err := f.Flatten(); err == nil {
			t.Fatalf("certification: flatten reported success while a stop submission was unconfirmed")
		}
		if exit := certExitPosts(f); exit != 1 {
			t.Fatalf("certification: operator exit posted %d exits, want exactly 1", exit)
		}
		f.AssertFlatAndNoRiskOrders(t)
		f.AssertHalted(t, "halted_unknown")
		if state := f.ReservationState(); state == "released" {
			t.Fatalf("certification: released the hold while the run is halted on an unconfirmed stop")
		}

	case crashAfterSLAccepted:
		if entry := certEntryPosts(f); entry != 1 {
			t.Fatalf("certification: venue saw %d entries, want exactly 1", entry)
		}
		if stops := certVenueRole(f, "sl"); stops != 1 {
			t.Fatalf("certification: venue owns %d stops, want exactly 1", stops)
		}
		f.Steps(8)
		view := f.run()
		if view.State != "position_open" {
			t.Fatalf("certification: recovered run is %q, want position_open", view.State)
		}
		if stops := certVenueRole(f, "sl"); stops != 1 {
			t.Fatalf("certification: recovery duplicated the stop (%d venue stops)", stops)
		}
		certAssertHoldKept(t, f, "while the recovered position is protected")
		f.AssertOneEntry(t, "signal-1")
		if err := f.Flatten(); err != nil {
			t.Fatalf("certification: recovered flatten: %v", err)
		}
		f.AssertFlatAndNoRiskOrders(t)
		f.AssertNoOpenOrders(t)
		if view = f.run(); view.State != "completed" {
			t.Fatalf("certification: recovered run is %q after the exit, want completed", view.State)
		}
		if state := f.ReservationState(); state != "released" {
			t.Fatalf("certification: reservation is %q after the trade closed, want released", state)
		}

	case crashAfterExitAccepted:
		if crashed.State != "position_open" {
			t.Fatalf("certification: crash after the exit acknowledgement left run %q, want position_open", crashed.State)
		}
		if exit := certExitPosts(f); exit != 1 {
			t.Fatalf("certification: venue saw %d exits, want exactly 1", exit)
		}
		f.Steps(8)
		view := f.run()
		if view.State != "completed" {
			t.Fatalf("certification: recovered run is %q with the position closed, want completed", view.State)
		}
		if exit := certExitPosts(f); exit != 1 {
			t.Fatalf("certification: recovery resent the exit (%d posts)", exit)
		}
		if entry := certEntryPosts(f); entry != 1 {
			t.Fatalf("certification: venue saw %d entries across the crash and restart, want exactly 1", entry)
		}
		f.AssertFlatAndNoRiskOrders(t)
		f.AssertNoOpenOrders(t)
		if state := f.ReservationState(); state != "released" {
			t.Fatalf("certification: reservation is %q after the recovered exit, want released", state)
		}

	default:
		t.Fatalf("certification: unknown breakpoint %q", step)
	}
	f.AssertAtMostOneEntry(t, "signal-1")
}

// waitForCertificationFile blocks until the child created its marker file.
func waitForCertificationFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("certification: %s did not appear within %s", filepath.Base(path), timeout)
}

// certDescribe keeps helper failures readable.
func certDescribe(output *bytes.Buffer) string { return strings.TrimSpace(output.String()) }
