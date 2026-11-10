package smartweb

import (
	"context"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

// NewAppLoginAuditor adapts the app-level login protection service to the
// transport LoginAuditor interface used by the login boundary.
func NewAppLoginAuditor(service *app.LoginProtectionService) LoginAuditor {
	if service == nil {
		return nil
	}
	return appLoginAuditor{service: service}
}

type appLoginAuditor struct {
	service *app.LoginProtectionService
}

func (a appLoginAuditor) CheckLock(ctx context.Context, loginName string) *coreerrors.Error {
	return a.service.CheckLock(ctx, loginName)
}

func (a appLoginAuditor) RecordFailure(ctx context.Context, loginName string, meta LoginTransportMeta) (string, bool) {
	hint, err := a.service.RecordFailure(ctx, loginName, app.LoginMeta{
		IP: meta.IP, UserAgent: meta.UserAgent, Device: meta.Device, RequestID: meta.RequestID,
	})
	if err != nil {
		// Audit failures must not block the credential boundary.
		return "", false
	}
	return hint, hint != ""
}

func (a appLoginAuditor) RecordSuccess(ctx context.Context, loginName string, meta LoginTransportMeta) {
	a.service.RecordSuccess(ctx, loginName, app.LoginMeta{
		IP: meta.IP, UserAgent: meta.UserAgent, Device: meta.Device, RequestID: meta.RequestID,
	})
}

func (a appLoginAuditor) RecordLogout(ctx context.Context, subject string, tenantID uint64, meta LoginTransportMeta) {
	a.service.RecordLogout(ctx, subject, tenantID, app.LoginMeta{
		IP: meta.IP, UserAgent: meta.UserAgent, Device: meta.Device, RequestID: meta.RequestID,
	})
}

// LoginAuditor receives SmartAdmin-compatible login lifecycle events. It is
// implemented by the level-3 login protection service; a nil auditor leaves
// login behavior unchanged.
type LoginAuditor interface {
	// CheckLock rejects an attempt while the resolved account is locked.
	CheckLock(ctx context.Context, loginName string) *coreerrors.Error
	// RecordFailure increments fail counters and returns a user-facing hint
	// (lock or remaining attempts). The boolean is true when the message
	// should replace the default invalid-credential text.
	RecordFailure(ctx context.Context, loginName string, meta LoginTransportMeta) (hint string, showHint bool)
	// RecordSuccess clears fail counters and writes the success log.
	RecordSuccess(ctx context.Context, loginName string, meta LoginTransportMeta)
	// RecordLogout writes one logout audit row.
	RecordLogout(ctx context.Context, subject string, tenantID uint64, meta LoginTransportMeta)
}

// LoginTransportMeta carries request metadata the auditor persists.
type LoginTransportMeta struct {
	IP        string
	UserAgent string
	Device    string
	RequestID string
}

type loginMetaContextKey struct{}

// WithLoginMeta attaches transport metadata to the context consumed by the
// login/logout boundary.
func WithLoginMeta(ctx context.Context, meta LoginTransportMeta) context.Context {
	return context.WithValue(ctx, loginMetaContextKey{}, meta)
}

func loginMetaFromContext(ctx context.Context) LoginTransportMeta {
	meta, _ := ctx.Value(loginMetaContextKey{}).(LoginTransportMeta)
	return meta
}

// SetLoginAuditor wires the optional login protection auditor.
func (s *Service) SetLoginAuditor(auditor LoginAuditor) {
	if s == nil {
		return
	}
	s.loginAuditor = auditor
}
