package app

import (
	"context"
	"os"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// HeartBeatRecord is one process heartbeat row.
type HeartBeatRecord struct {
	ID               uint64    `json:"heartBeatRecordId"`
	ProjectPath      string    `json:"projectPath"`
	ServerIP         string    `json:"serverIp"`
	ProcessNo        int       `json:"processNo"`
	ProcessStartTime time.Time `json:"processStartTime"`
	HeartBeatTime    time.Time `json:"heartBeatTime"`
}

// HeartBeatPage is the {list,total} envelope.
type HeartBeatPage struct {
	List  []HeartBeatRecord `json:"list"`
	Total int               `json:"total"`
}

// HeartBeatQuery carries console filters.
type HeartBeatQuery struct {
	Keywords  string
	StartDate string
	EndDate   string
	PageNum   uint64
	PageSize  uint64
}

// HeartBeatStore persists heartbeat rows.
type HeartBeatStore interface {
	List(ctx context.Context, query HeartBeatQuery) (HeartBeatPage, error)
	Upsert(ctx context.Context, record HeartBeatRecord) error
}

// HeartBeatService queries process heartbeats and emits them on a fixed
// schedule. The upsert key is (projectPath, serverIp, processNo).
type HeartBeatService struct {
	store      HeartBeatStore
	authorizer RBACAuthorizer
	now        func() time.Time

	identity  heartbeatIdentity
	startedAt time.Time
}

type heartbeatIdentity struct {
	projectPath string
	serverIP    string
	processNo   int
}

// NewHeartBeatService constructs the heartbeat service, capturing the
// process identity at wiring time.
func NewHeartBeatService(store HeartBeatStore, serverIP string, authorizer RBACAuthorizer) *HeartBeatService {
	now := time.Now()
	projectPath, err := os.Getwd()
	if err != nil {
		projectPath = ""
	}
	return &HeartBeatService{
		store:      store,
		authorizer: authorizer,
		now:        time.Now,
		startedAt:  now,
		identity: heartbeatIdentity{
			projectPath: projectPath,
			serverIP:    serverIP,
			processNo:   os.Getpid(),
		},
	}
}

// Record writes one heartbeat upsert.
func (s *HeartBeatService) Record(ctx context.Context) error {
	now := s.now()
	return s.store.Upsert(ctx, HeartBeatRecord{
		ProjectPath:      s.identity.projectPath,
		ServerIP:         s.identity.serverIP,
		ProcessNo:        s.identity.processNo,
		ProcessStartTime: s.startedAt,
		HeartBeatTime:    now,
	})
}

// Run emits a heartbeat every interval after an initial delay, until ctx
// is canceled. Defaults mirror the Java manager (60s initial, 300s tick).
func (s *HeartBeatService) Run(ctx context.Context, initialDelay, interval time.Duration) {
	if interval <= 0 {
		interval = 300 * time.Second
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_ = s.Record(ctx)
			timer.Reset(interval)
		}
	}
}

// List returns a heartbeat page.
func (s *HeartBeatService) List(ctx context.Context, query HeartBeatQuery) (HeartBeatPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportDevToolsRead); err != nil {
		return HeartBeatPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, query)
	if storeErr != nil {
		return HeartBeatPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list heartbeats", storeErr)
	}
	return page, nil
}
