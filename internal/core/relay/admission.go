package relay

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AdmissionTicket contains a one-use routing secret, signed by the issuing
// device. Keep it out of cached cloud images, diagnostics, URLs and log output.
// The owner fingerprint must be compared independently at the actual session.
type AdmissionTicket struct {
	Schema       int            `json:"schema"`
	ID           string         `json:"id"`
	Task         CloudTask      `json:"task"`
	Generation   int64          `json:"generation"`
	Origin       string         `json:"origin"`
	Ticket       string         `json:"ticket"`
	Provider     string         `json:"provider"`
	Session      string         `json:"session"`
	Expires      int64          `json:"expires"`
	LeaseSeconds int            `json:"leaseSeconds"`
	Owner        PublicIdentity `json:"owner"`
	Signature    []byte         `json:"signature"`
}

var admissionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func admissionProvider(provider string) bool {
	return provider == "claude-hosted" || provider == "codex-current" || provider == "codex-legacy" || provider == "work-cloud"
}

func (t AdmissionTicket) signed() []byte {
	t.Signature = nil
	body, _ := json.Marshal(t)
	return append([]byte("hopsesh-cloud-invitation-v2\x00"), body...)
}

func (t AdmissionTicket) Verify(fingerprint string) error {
	if t.Schema != 2 || t.Generation < 1 || t.Generation >= 1<<53 || !taskID(t.ID) || t.Task.Verify(fingerprint) != nil || t.Task.Provider != t.Provider || t.Owner.Check() != nil || fingerprint == "" || t.Owner.ID != fingerprint || t.Owner.Endpoint == "" || strings.HasPrefix(t.Owner.Endpoint, "cloud/") || !validDeviceCode(t.Ticket) || !admissionProvider(t.Provider) || !admissionName.MatchString(t.Session) || t.LeaseSeconds < 60 || t.LeaseSeconds > int(MaxLifetime/time.Second) || t.Expires < 1 || len(t.Signature) != ed25519.SignatureSize || !ed25519.Verify(t.Owner.Signing, t.signed(), t.Signature) {
		return errors.New("cloud admission signature, independently checked owner or scope is invalid")
	}
	origin, err := (Enrollment{Origin: t.Origin}).origin()
	if err != nil || origin != t.Origin {
		return errors.New("cloud admission origin is not canonical verified HTTPS")
	}
	return nil
}

func validateAdmissionRequest(identity Identity, connection Connection, provider, session string, lease time.Duration) error {
	if identity.Check() != nil || identity.Public.Endpoint == "" || strings.HasPrefix(identity.Public.Endpoint, "cloud/") || connection.Device != identity.Public.ID || connection.Expires <= time.Now().Add(time.Minute).Unix() || !admissionProvider(provider) || !admissionName.MatchString(session) || lease < time.Minute || lease > MaxLifetime {
		return errors.New("cloud admission needs an enrolled native device, a real provider/session ID and a bounded lease")
	}
	_, err := (Enrollment{Origin: connection.URL}).origin()
	return err
}

func issueAdmission(ctx context.Context, identity Identity, connection Connection, provider, session string, lease time.Duration, id string, task CloudTask, generation int64) (AdmissionTicket, error) {
	var ticket AdmissionTicket
	if generation < 1 || generation >= 1<<53 || !taskID(id) || task.Verify(identity.Public.ID) != nil || task.Provider != provider || validateAdmissionRequest(identity, connection, provider, session, lease) != nil {
		return ticket, errors.New("cloud admission needs an enrolled native device, a real provider/session ID and a bounded lease")
	}
	httpClient, err := connection.HTTPClient()
	if err != nil {
		return ticket, err
	}
	if httpClient != nil {
		defer httpClient.CloseIdleConnections()
	}
	client := Enrollment{Origin: connection.URL, HTTP: httpClient}
	origin, err := client.origin()
	if err != nil {
		return ticket, err
	}
	var response struct {
		Ticket   string `json:"ticket"`
		Provider string `json:"provider"`
		Session  string `json:"session"`
		Expires  int64  `json:"expires"`
		Lease    int    `json:"lease_seconds"`
	}
	seconds := int(lease / time.Second)
	status, err := client.requestAuthorized(ctx, "/v1/cloud/tickets", url.Values{"provider": {provider}, "session": {session}, "lease_seconds": {strconv.Itoa(seconds)}}, &response, connection.Token)
	if err != nil {
		return ticket, err
	}
	if status != http.StatusCreated || !validDeviceCode(response.Ticket) || response.Provider != provider || response.Session != session || response.Lease != seconds || response.Expires <= time.Now().Unix() || response.Expires > time.Now().Add(11*time.Minute).Unix() {
		return ticket, errors.New("relay refused or returned an invalid cloud admission ticket")
	}
	ticket = AdmissionTicket{Schema: 2, ID: id, Task: task, Generation: generation, Origin: origin, Ticket: response.Ticket, Provider: provider, Session: session, Expires: response.Expires, LeaseSeconds: seconds, Owner: identity.Public}
	ticket.Signature = ed25519.Sign(identity.Signing, ticket.signed())
	return ticket, nil
}

type CloudClaim struct {
	Provider    string
	Session     string
	Incarnation string
	Expires     int64
}

func ClaimAdmission(ctx context.Context, ticket AdmissionTicket, ownerFingerprint string, identity Identity, scope CloudClaim, httpClient *http.Client) (Connection, error) {
	if err := ticket.Verify(ownerFingerprint); err != nil {
		return Connection{}, err
	}
	if ticket.Expires <= time.Now().Unix() || ticket.Expires > time.Now().Add(11*time.Minute).Unix() || scope.Provider != ticket.Provider || scope.Session != ticket.Session || len(scope.Incarnation) != 32 || strings.Trim(scope.Incarnation, "abcdef0123456789") != "" || scope.Expires <= time.Now().Unix() || scope.Expires > time.Now().Add(MaxLifetime).Unix() || identity.Check() != nil || identity.Public.Endpoint != "cloud/"+scope.Provider+"/"+scope.Incarnation {
		return Connection{}, errors.New("cloud admission does not match this fresh session incarnation or lease")
	}
	public, err := json.Marshal(identity.Public)
	if err != nil {
		return Connection{}, err
	}
	values := url.Values{"ticket": {ticket.Ticket}, "identity": {string(public)}, "provider": {scope.Provider}, "session": {scope.Session}, "incarnation": {scope.Incarnation}, "lease_expires": {strconv.FormatInt(scope.Expires, 10)}}
	message := "hopsesh-cloud-admission-v1\x00" + ticket.Origin
	for _, key := range []string{"ticket", "identity", "provider", "session", "incarnation", "lease_expires"} {
		message += "\x00" + values.Get(key)
	}
	values.Set("proof", base64.StdEncoding.EncodeToString(ed25519.Sign(identity.Signing, []byte(message))))
	client := Enrollment{Origin: ticket.Origin, HTTP: httpClient}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(min(ticket.Expires, scope.Expires), 0))
	defer cancel()
	for delay := time.Second; ; delay = min(8*time.Second, delay*2) {
		var out struct {
			Connection
			Kind        string `json:"kind"`
			Provisional bool   `json:"provisional"`
			Error       string `json:"error"`
		}
		status, err := client.request(ctx, "/v1/cloud/claim", values, &out)
		if err == nil && status == http.StatusOK {
			if out.Kind != "cloud-session" || !out.Provisional || out.Device != identity.Public.ID || !opaque(out.Space) || out.Token == "" || len(out.Token) > 4096 || strings.ContainsAny(out.Token, "\r\n") || out.Expires <= time.Now().Unix() || out.Expires > scope.Expires || out.Expires > time.Now().Add(time.Duration(ticket.LeaseSeconds)*time.Second).Unix() || out.CAFile != "" || out.URL != "" {
				return Connection{}, errors.New("relay returned a widened cloud admission credential")
			}
			out.URL = ticket.Origin
			return out.Connection, nil
		}
		if err != nil && !errors.Is(err, errEnrollmentNetwork) && !errors.Is(err, errEnrollmentTemporary) {
			return Connection{}, err
		}
		if err == nil && status < 500 && status != http.StatusTooManyRequests {
			if status == http.StatusConflict && out.Error == "capacity_exceeded" {
				return Connection{}, errors.New("cloud admission refused: the relay space has reached its device limit; wait for unused routing leases to expire, then retry this invitation if it is still valid")
			}
			code := "refused"
			switch out.Error {
			case "invalid_request", "invalid_identity", "invalid_proof", "invalid_grant", "access_denied", "already_claimed", "expired_token":
				code = out.Error
			}
			return Connection{}, fmt.Errorf("cloud admission refused (HTTP %d, %s); check expiry, revocation, session binding and prior claims", status, code)
		}
		if err = client.pause(ctx, retryDelay(delay, err)); err != nil {
			return Connection{}, err
		}
	}
}

func RevokeAdmission(ctx context.Context, connection Connection, ticket AdmissionTicket) error {
	if err := ticket.Verify(connection.Device); err != nil {
		return err
	}
	origin, err := (Enrollment{Origin: connection.URL}).origin()
	if err != nil || origin != ticket.Origin {
		return errors.New("cloud admission belongs to another relay; use its original relay connection")
	}
	client, err := connection.HTTPClient()
	if err != nil {
		return err
	}
	if client != nil {
		defer client.CloseIdleConnections()
	}
	var out struct {
		Revoked bool `json:"revoked"`
	}
	status, err := (Enrollment{Origin: ticket.Origin, HTTP: client}).requestAuthorized(ctx, "/v1/cloud/revoke", url.Values{"ticket": {ticket.Ticket}}, &out, connection.Token)
	if err != nil {
		return err
	}
	if status != http.StatusOK || !out.Revoked {
		return errors.New("cloud admission revocation was not confirmed; retry before relying on it")
	}
	return nil
}

type AdmissionStatus struct {
	Superseded   bool            `json:"superseded,omitempty"`
	Status       string          `json:"status"`
	Provider     string          `json:"provider"`
	Session      string          `json:"session"`
	Expires      int64           `json:"expires"`
	LeaseExpires int64           `json:"leaseExpires"`
	Public       *PublicIdentity `json:"public"`
}

func CheckAdmission(ctx context.Context, c Connection, ticket AdmissionTicket) (AdmissionStatus, error) {
	var out AdmissionStatus
	if err := ticket.Verify(c.Device); err != nil {
		return out, err
	}
	origin, err := (Enrollment{Origin: c.URL}).origin()
	if err != nil || origin != ticket.Origin {
		return out, errors.New("cloud invitation belongs to another relay")
	}
	client, err := c.HTTPClient()
	if err != nil {
		return out, err
	}
	if client != nil {
		defer client.CloseIdleConnections()
	}
	status, err := (Enrollment{Origin: ticket.Origin, HTTP: client}).requestAuthorized(ctx, "/v1/cloud/status", url.Values{"ticket": {ticket.Ticket}}, &out, c.Token)
	if err != nil {
		return out, err
	}
	// Supersession is native historical authority, never a server assertion.
	out.Superseded = false
	valid := out.Status == "pending" || out.Status == "claimed" || out.Status == "revoked" || out.Status == "expired"
	if status != http.StatusOK || !valid || out.Provider != ticket.Provider || out.Session != ticket.Session || out.Expires != ticket.Expires || out.LeaseExpires < 0 || out.LeaseExpires > ticket.Expires+int64(ticket.LeaseSeconds) {
		return AdmissionStatus{}, errors.New("cloud claim status could not be confirmed")
	}
	if out.Public != nil {
		prefix := "cloud/" + ticket.Provider + "/"
		if out.Public.Check() != nil || !strings.HasPrefix(out.Public.Endpoint, prefix) || len(strings.TrimPrefix(out.Public.Endpoint, prefix)) != 32 {
			return AdmissionStatus{}, errors.New("cloud claim returned an invalid session identity")
		}
	}
	if out.Status == "claimed" && (out.Public == nil || out.LeaseExpires <= time.Now().Unix()) {
		return AdmissionStatus{}, errors.New("claimed cloud delivery is no longer active")
	}
	return out, nil
}
