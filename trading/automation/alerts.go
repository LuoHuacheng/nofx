package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"syscall"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Alert is one operator-visible event. It never carries a credential: the
// message is redacted before it is queued, and again before it is sent.
type Alert struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	AccountKey string    `gorm:"column:account_key;not null;index" json:"account_key"`
	Code       string    `gorm:"column:code;not null" json:"code"`
	Severity   string    `gorm:"column:severity;not null" json:"severity"`
	Message    string    `gorm:"column:message;not null" json:"message"`
	CreatedAt  time.Time `gorm:"column:created_at;not null" json:"created_at"`

	Attempts    int        `gorm:"column:attempts;not null" json:"-"`
	NextAttempt time.Time  `gorm:"column:next_attempt;not null" json:"-"`
	DeliveredAt *time.Time `gorm:"column:delivered_at" json:"-"`
}

func (Alert) TableName() string { return "managed_alert_outbox" }

// alertTarget is the single administrator-configured webhook. The row id is
// fixed so an operator can only replace the target, never add a second one.
type alertTarget struct {
	ID  uint   `gorm:"primaryKey"`
	URL string `gorm:"column:url;not null"`
}

func (alertTarget) TableName() string { return "managed_alert_targets" }

const (
	AlertSeverityInfo     = "info"
	AlertSeverityCritical = "critical"
)

// alertBackoff is the retry cadence from the plan: 1s, 5s, 30s, then every 60s.
var alertBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

var alertSecretPattern = regexp.MustCompile(`(?i)(api[_ -]?key|secret[_ -]?key|passphrase|signature|authorization|bearer|jwt)\s*[:=]?\s*[^\s",}]+`)

// redact strips credential-shaped fragments from an operator message. It is a
// best-effort scrubber, not a guarantee: callers must still avoid formatting
// secrets into messages in the first place.
func redact(message string) string {
	return alertSecretPattern.ReplaceAllString(message, "$1=[redacted]")
}

// QueueAlert persists an alert before it is delivered, so a crash between the
// fault and the notification still leaves the evidence behind.
func (r *Runtime) QueueAlert(ctx context.Context, a Alert) error {
	if a.AccountKey == "" || a.Code == "" {
		return errors.New("automation: an alert requires an account and a code")
	}
	if a.Severity == "" {
		a.Severity = AlertSeverityCritical
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = r.clock()
	}
	a.Message = redact(a.Message)
	a.NextAttempt = a.CreatedAt
	return r.db.WithContext(ctx).Create(&a).Error
}

// SetAlertTarget installs the single webhook target. Only an https endpoint
// outside private address space is accepted; the actual IP is checked when the
// connection is dialled, so a DNS answer that changes after validation is
// refused instead of trusted.
func (r *Runtime) SetAlertTarget(raw string) error {
	target, err := validateAlertTarget(raw, nil)
	if err != nil {
		return err
	}
	if err := r.db.AutoMigrate(&alertTarget{}); err != nil {
		return err
	}
	row := alertTarget{ID: 1, URL: target}
	if err := r.db.WithContext(context.Background()).
		Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"url"})}).
		Create(&row).Error; err != nil {
		return err
	}
	r.mu.Lock()
	r.alertURL = target
	r.mu.Unlock()
	return nil
}

func (r *Runtime) alertEndpoint() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alertURL
}

// DeliverAlerts sends due alerts in order. A failure only postpones the alert:
// it never blocks protection supervision and never clears a breaker latch.
func (r *Runtime) DeliverAlerts(ctx context.Context) error {
	endpoint := r.alertEndpoint()
	if endpoint == "" {
		var stored alertTarget
		if err := r.db.WithContext(ctx).Where("id = ?", 1).First(&stored).Error; err == nil {
			endpoint = stored.URL
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if endpoint == "" {
		return nil
	}
	var due []Alert
	if err := r.db.WithContext(ctx).
		Where("delivered_at IS NULL AND next_attempt <= ?", r.clock()).
		Order("id").Limit(50).Find(&due).Error; err != nil {
		return err
	}
	var failures []error
	for _, a := range due {
		at := r.clock()
		if err := r.postAlert(ctx, endpoint, a); err != nil {
			attempt := a.Attempts + 1
			wait := 60 * time.Second
			if attempt <= len(alertBackoff) {
				wait = alertBackoff[attempt-1]
			}
			if e := r.db.WithContext(ctx).Model(&Alert{}).Where("id = ?", a.ID).
				Updates(map[string]any{"attempts": attempt, "next_attempt": at.Add(wait)}).Error; e != nil {
				failures = append(failures, e)
			}
			failures = append(failures, fmt.Errorf("automation: alert %d not delivered: %w", a.ID, err))
			continue
		}
		if e := r.db.WithContext(ctx).Model(&Alert{}).Where("id = ?", a.ID).
			Updates(map[string]any{"delivered_at": at, "attempts": a.Attempts + 1}).Error; e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}

type alertPayload struct {
	AccountKey string    `json:"account_key"`
	Code       string    `json:"code"`
	Severity   string    `json:"severity"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

func (r *Runtime) postAlert(ctx context.Context, endpoint string, a Alert) error {
	body, err := json.Marshal(alertPayload{AccountKey: a.AccountKey, Code: a.Code, Severity: a.Severity, Message: redact(a.Message), CreatedAt: a.CreatedAt})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.alertClient
	if client == nil {
		client = newAlertClient(nil)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("automation: webhook answered %d", resp.StatusCode)
	}
	return nil
}

// validateAlertTarget rejects non-https, loopback, private and link-local
// endpoints. A nil resolver means the real one.
func validateAlertTarget(raw string, resolve func(string) ([]net.IP, error)) (string, error) {
	if raw == "" {
		return "", errors.New("automation: an alert target is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("automation: alert target is not a URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", errors.New("automation: alert target must use https")
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("automation: alert target has no host")
	}
	if resolve == nil {
		resolve = net.LookupIP
	}
	ips, err := resolve(host)
	if err != nil {
		return "", fmt.Errorf("automation: alert target does not resolve: %w", err)
	}
	if len(ips) == 0 {
		return "", errors.New("automation: alert target resolves to nothing")
	}
	for _, ip := range ips {
		if !publicAlertIP(ip) {
			return "", fmt.Errorf("automation: alert target resolves to a non-public address (%s)", ip)
		}
	}
	return u.String(), nil
}

func publicAlertIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 0 {
		return false
	}
	return true
}

// guardDial is the connection-level check: the IP actually dialled must still
// be public, which is what defeats a DNS answer that changed after validation.
func guardDial(control func(network, address string, c syscall.RawConn) error) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !publicAlertIP(ip) {
			return fmt.Errorf("automation: refusing to connect to %s", host)
		}
		if control != nil {
			return control(network, address, c)
		}
		return nil
	}
}

// newAlertClient is the only client used for alert delivery. It re-checks the
// address at dial time and refuses every redirect: the target was validated
// once, so no later hop may carry the body to another host or to plaintext.
func newAlertClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second, Control: guardDial(control)}).DialContext,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
