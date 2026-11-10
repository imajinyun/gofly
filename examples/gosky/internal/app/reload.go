package app

import (
	"context"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/runtimecache"
)

// Reload tags mirror ReloadConst from the Java reference implementation.
const (
	ReloadTagCacheService = "cache_service"
	ReloadTagSystemConfig = "system_config"
)

// ReloadItem is one code-owned heartbeat row.
type ReloadItem struct {
	Tag            string    `json:"tag"`
	Args           string    `json:"args"`
	Identification string    `json:"identification"`
	CreatedAt      time.Time `json:"createTime"`
	UpdatedAt      time.Time `json:"updateTime"`
}

// ReloadResult is one execution audit row.
type ReloadResult struct {
	ResultID       uint64    `json:"-"`
	Tag            string    `json:"tag"`
	Identification string    `json:"identification"`
	Args           string    `json:"args"`
	Result         bool      `json:"result"`
	Exception      string    `json:"exception"`
	CreatedAt      time.Time `json:"createTime"`
}

// ReloadForm is the console update payload.
type ReloadForm struct {
	Tag            string `json:"tag"`
	Identification string `json:"identification"`
	Args           string `json:"args"`
}

// ReloadStore is the persistence boundary.
type ReloadStore interface {
	ListItems(ctx context.Context) ([]ReloadItem, error)
	UpdateItem(ctx context.Context, form ReloadForm) (bool, error)
	InsertResult(ctx context.Context, result ReloadResult) error
	ListResults(ctx context.Context, tag string, limit int) ([]ReloadResult, error)
}

// ReloadCommand executes one code-owned reload tag.
type ReloadCommand func(ctx context.Context, args string) error

// ReloadService owns the SmartReload heartbeat: a daemon polls item rows,
// and when an item's identification changes it invokes the registered
// command and records the result. The cache_service command clears named
// in-process caches; system_config is a code-owned no-op (gosky reads
// support configs from the database on every request).
type ReloadService struct {
	store      ReloadStore
	registry   *runtimecache.Registry
	authorizer RBACAuthorizer
	now        func() time.Time

	mu           sync.RWMutex
	identifiers  map[string]string
	results      map[string][]ReloadResult
	resultsLimit int
	commands     map[string]ReloadCommand
}

// NewReloadService constructs the reload service with its default command
// set wired to the process cache registry.
func NewReloadService(store ReloadStore, registry *runtimecache.Registry, authorizer RBACAuthorizer) *ReloadService {
	service := &ReloadService{
		store:        store,
		registry:     registry,
		authorizer:   authorizer,
		now:          time.Now,
		identifiers:  make(map[string]string),
		results:      make(map[string][]ReloadResult),
		resultsLimit: 50,
		commands:     make(map[string]ReloadCommand),
	}
	service.registerDefaults()
	return service
}

func (s *ReloadService) registerDefaults() {
	s.commands[ReloadTagCacheService] = func(_ context.Context, args string) error {
		name := strings.TrimSpace(args)
		if name == "" {
			if s.registry == nil {
				return nil
			}
			return s.registry.ClearAll()
		}
		cache, ok := s.registry.Get(name)
		if !ok {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "不能从系统中找到对应的缓存："+name)
		}
		return cache.Clear()
	}
	s.commands[ReloadTagSystemConfig] = func(context.Context, string) error {
		// support configs are read from MySQL on every request; nothing is
		// cached in process, so the heartbeat reports success without work.
		return nil
	}
}

// RegisterCommand installs a code-owned reload command, replacing any
// previous registration for the tag.
func (s *ReloadService) RegisterCommand(tag string, command ReloadCommand) {
	if s == nil || strings.TrimSpace(tag) == "" || command == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands[tag] = command
}

// Init loads current identifications so the first poll does not replay
// every seeded tag.
func (s *ReloadService) Init(ctx context.Context) error {
	items, err := s.store.ListItems(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		s.identifiers[item.Tag] = item.Identification
	}
	return nil
}

// PollOnce runs one identification-change scan; it is invoked by the daemon
// ticker and directly by tests.
func (s *ReloadService) PollOnce(ctx context.Context) error {
	items, err := s.store.ListItems(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		s.mu.Lock()
		previous, seen := s.identifiers[item.Tag]
		if seen && previous == item.Identification {
			s.mu.Unlock()
			continue
		}
		s.identifiers[item.Tag] = item.Identification
		command, known := s.commands[item.Tag]
		s.mu.Unlock()

		result := ReloadResult{
			Tag:            item.Tag,
			Identification: item.Identification,
			Args:           item.Args,
			Result:         true,
			CreatedAt:      s.now(),
		}
		if !known {
			result.Result = false
			result.Exception = "不能从系统中找到对应的tag：" + item.Tag
		} else if err := command(ctx, item.Args); err != nil {
			result.Result = false
			result.Exception = truncateRunes(err.Error(), 2000)
		}
		_ = s.store.InsertResult(ctx, result)
		s.appendResult(result)
	}
	return nil
}

func (s *ReloadService) appendResult(result ReloadResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := append(s.results[result.Tag], result)
	if len(results) > s.resultsLimit {
		results = results[len(results)-s.resultsLimit:]
	}
	s.results[result.Tag] = results
}

// Run polls until ctx is canceled.
func (s *ReloadService) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := s.Init(ctx); err != nil {
		// The first tick retries initialization; a missing table must not
		// crash the process.
		_ = err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.PollOnce(ctx)
		}
	}
}

// ---------------- support console ----------------

// ListItems serves the reload console item table.
func (s *ReloadService) ListItems(ctx context.Context) ([]ReloadItem, *coreerrors.Error) {
	items, err := s.store.ListItems(ctx)
	if err != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list reload items", err)
	}
	return items, nil
}

// ListResults serves the per-tag execution result modal.
func (s *ReloadService) ListResults(ctx context.Context, tag string) ([]ReloadResult, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportReloadRead); err != nil {
		return nil, err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "tag is required")
	}
	results, err := s.store.ListResults(ctx, tag, s.resultsLimit)
	if err != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list reload results", err)
	}
	return results, nil
}

// UpdateItem bumps an item's identification, which the next poll detects.
func (s *ReloadService) UpdateItem(ctx context.Context, form ReloadForm) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportReloadManage); err != nil {
		return err
	}
	form.Tag = strings.TrimSpace(form.Tag)
	form.Identification = strings.TrimSpace(form.Identification)
	if form.Tag == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "标签不能为空")
	}
	if form.Identification == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "状态标识不能为空")
	}
	if len([]rune(form.Args)) > 1000 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "参数过长")
	}
	updated, err := s.store.UpdateItem(ctx, form)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update reload item", err)
	}
	if !updated {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return nil
}
