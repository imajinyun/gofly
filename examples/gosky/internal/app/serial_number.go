package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Serial rule periods. NONE never resets; the others reset the counter to
// init_number on the first generation after the period boundary.
const (
	SerialRuleNone  = "NONE"
	SerialRuleYear  = "YEAR"
	SerialRuleMonth = "MONTH"
	SerialRuleDay   = "DAY"
)

// SerialNumber is one deployment-owned serial definition.
type SerialNumber struct {
	ID              uint64     `json:"serialNumberId"`
	BusinessName    string     `json:"businessName"`
	Format          string     `json:"format"`
	RuleType        string     `json:"ruleType"`
	InitNumber      int64      `json:"initNumber"`
	StepRandomRange int        `json:"stepRandomRange"`
	LastNumber      *int64     `json:"lastNumber"`
	LastTime        *time.Time `json:"lastTime"`
	Remark          string     `json:"remark"`
	CreatedAt       time.Time  `json:"createTime"`
	UpdatedAt       time.Time  `json:"updateTime"`
}

// SerialNumberRecord is the per-day generation summary shown in the UI.
type SerialNumberRecord struct {
	ID             uint64    `json:"-"`
	SerialNumberID uint64    `json:"serialNumberId"`
	RecordDate     string    `json:"recordDate"`
	Count          int       `json:"count"`
	LastNumber     int64     `json:"lastNumber"`
	LastTime       time.Time `json:"lastTime"`
}

// SerialNumberRecordPage is the {list,total} envelope for record queries.
type SerialNumberRecordPage struct {
	List  []SerialNumberRecord `json:"list"`
	Total int                  `json:"total"`
}

type SerialNumberGenerateInput struct {
	SerialNumberID uint64 `json:"serialNumberId"`
	Count          int    `json:"count"`
}

// SerialNumberCompute advances a locked definition and returns the raw
// numbers plus the new last value. It runs inside the repository row lock.
type SerialNumberCompute func(definition SerialNumber, now time.Time, count int) (numbers []int64, last int64)

// SerialNumberStore is the row-locked persistence boundary.
type SerialNumberStore interface {
	ListDefinitions(ctx context.Context) ([]SerialNumber, error)
	// GenerateInTx locks the definition, runs compute under the lock, and
	// persists the new last number plus the daily summary atomically.
	GenerateInTx(ctx context.Context, id uint64, count int, now time.Time, compute SerialNumberCompute) (SerialNumber, []int64, error)
	ListRecords(ctx context.Context, id uint64, pageNum, pageSize uint64) (SerialNumberRecordPage, error)
}

// SerialNumberService owns the DB-lock number generator.
type SerialNumberService struct {
	store      SerialNumberStore
	authorizer RBACAuthorizer
	now        func() time.Time
	randomStep func(upperBound int) int
}

func NewSerialNumberService(store SerialNumberStore, authorizer RBACAuthorizer) *SerialNumberService {
	return &SerialNumberService{
		store:      store,
		authorizer: authorizer,
		now:        time.Now,
		randomStep: randomIntInclusive,
	}
}

// Generate allocates count serial numbers for one definition inside the
// repository row lock. The returned strings are the formatted numbers.
func (s *SerialNumberService) Generate(ctx context.Context, input SerialNumberGenerateInput) ([]string, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportSerialGenerate); err != nil {
		return nil, err
	}
	if input.SerialNumberID == 0 {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "serial number id is required")
	}
	if input.Count <= 0 || input.Count > 1000 {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "count must be between 1 and 1000")
	}
	now := s.now()
	definition, numbers, err := s.store.GenerateInTx(ctx, input.SerialNumberID, input.Count, now,
		func(locked SerialNumber, at time.Time, count int) ([]int64, int64) {
			return s.compute(locked, at, count)
		})
	if err != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "generate serial number", err)
	}
	if !validSerialRule(definition.RuleType) || !validSerialFormat(definition.Format) {
		return nil, coreerrors.New(coreerrors.CodeFailedPrecondition, "serial number definition is misconfigured")
	}
	numberToken, digitCount, hasYear, hasMonth, hasDay := parseSerialFormat(definition.Format)
	formatted := make([]string, 0, len(numbers))
	for _, number := range numbers {
		formatted = append(formatted, formatSerialNumber(definition.Format, numberToken, digitCount, hasYear, hasMonth, hasDay, now, number))
	}
	return formatted, nil
}

// compute applies the reset and stepping rules to one locked definition.
func (s *SerialNumberService) compute(definition SerialNumber, now time.Time, count int) ([]int64, int64) {
	last := definition.InitNumber
	if definition.LastNumber != nil && !periodReset(definition, now) {
		last = *definition.LastNumber
	}
	numbers := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		step := int64(1)
		if definition.StepRandomRange > 1 {
			step = int64(s.randomStep(definition.StepRandomRange))
		}
		last += step
		numbers = append(numbers, last)
	}
	return numbers, last
}

// ListDefinitions returns all definitions for the console table.
func (s *SerialNumberService) ListDefinitions(ctx context.Context) ([]SerialNumber, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportSerialRead); err != nil {
		return nil, err
	}
	items, err := s.store.ListDefinitions(ctx)
	if err != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list serial numbers", err)
	}
	return items, nil
}

// ListRecords returns paged daily summaries for one definition.
func (s *SerialNumberService) ListRecords(ctx context.Context, id uint64, pageNum, pageSize uint64) (SerialNumberRecordPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportSerialRead); err != nil {
		return SerialNumberRecordPage{}, err
	}
	if id == 0 {
		return SerialNumberRecordPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "serial number id is required")
	}
	pageNum, pageSize = NormalizePage(pageNum, pageSize)
	page, err := s.store.ListRecords(ctx, id, pageNum, pageSize)
	if err != nil {
		return SerialNumberRecordPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list serial records", err)
	}
	return page, nil
}

func validSerialRule(rule string) bool {
	switch strings.ToUpper(strings.TrimSpace(rule)) {
	case SerialRuleNone, SerialRuleYear, SerialRuleMonth, SerialRuleDay:
		return true
	}
	return false
}

func validSerialFormat(format string) bool {
	open := strings.Index(format, "[n")
	close := strings.Index(format, "n]")
	return open >= 0 && close > open
}

// periodReset reports whether the last generation belongs to a previous
// reset period relative to now.
func periodReset(definition SerialNumber, now time.Time) bool {
	if definition.LastTime == nil {
		return true
	}
	last := *definition.LastTime
	switch strings.ToUpper(definition.RuleType) {
	case SerialRuleYear:
		return last.Year() != now.Year()
	case SerialRuleMonth:
		return last.Year() != now.Year() || last.Month() != now.Month()
	case SerialRuleDay:
		return last.Year() != now.Year() || last.YearDay() != now.YearDay()
	default:
		return false
	}
}

// parseSerialFormat extracts the number placeholder ("[nnn]"), the digit
// count, and the date placeholder flags.
func parseSerialFormat(format string) (numberToken string, digitCount int, hasYear, hasMonth, hasDay bool) {
	open := strings.Index(format, "[n")
	close := strings.Index(format, "n]")
	if open >= 0 && close > open {
		numberToken = format[open : close+2]
		digitCount = close - open
	}
	hasYear = strings.Contains(format, "[yyyy]")
	hasMonth = strings.Contains(format, "[mm]")
	hasDay = strings.Contains(format, "[dd]")
	return
}

func formatSerialNumber(format, numberToken string, digitCount int, hasYear, hasMonth, hasDay bool, now time.Time, number int64) string {
	rendered := format
	if hasYear {
		rendered = strings.ReplaceAll(rendered, "[yyyy]", itoaWidth(int64(now.Year()), 4))
	}
	if hasMonth {
		rendered = strings.ReplaceAll(rendered, "[mm]", twoDigit(int(now.Month())))
	}
	if hasDay {
		rendered = strings.ReplaceAll(rendered, "[dd]", twoDigit(now.Day()))
	}
	return strings.ReplaceAll(rendered, numberToken, padZero(number, digitCount))
}

func twoDigit(value int) string {
	if value < 10 {
		return "0" + itoaWidth(int64(value), 1)
	}
	return itoaWidth(int64(value), 2)
}
