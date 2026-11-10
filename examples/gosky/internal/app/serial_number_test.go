package app

import (
	"context"
	"errors"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory:
//
//	SR-01 sequential generation from init number
//	SR-02 period reset (YEAR/MONTH/DAY) restarts at init number
//	SR-03 NONE rule never resets
//	SR-04 random stepping advances by 1..range
//	SR-05 batch formatting with date/zero-pad placeholders
//	SR-06 capability and input validation
//	SR-07 repository failure maps to unavailable
type fakeSerialStore struct {
	definition SerialNumber
	records    map[uint64][]SerialNumberRecord
	advanced   bool
	fail       error
}

func (s *fakeSerialStore) ListDefinitions(_ context.Context) ([]SerialNumber, error) {
	return []SerialNumber{s.definition}, s.fail
}

func (s *fakeSerialStore) GenerateInTx(_ context.Context, _ uint64, count int, now time.Time, compute SerialNumberCompute) (SerialNumber, []int64, error) {
	if s.fail != nil {
		return SerialNumber{}, nil, s.fail
	}
	numbers, last := compute(s.definition, now, count)
	s.definition.LastNumber = &last
	zero := int64(0)
	_ = zero
	s.advanced = true
	return s.definition, numbers, nil
}

func (s *fakeSerialStore) ListRecords(_ context.Context, id uint64, _, _ uint64) (SerialNumberRecordPage, error) {
	return SerialNumberRecordPage{List: s.records[id], Total: len(s.records[id])}, s.fail
}

func serialCtx(caps ...authz.Capability) context.Context {
	return fileCtx(caps...)
}

func serialService(store SerialNumberStore, caps ...authz.Capability) *SerialNumberService {
	service := NewSerialNumberService(store, dictCapAuthorizer{allowed: capSet(caps)})
	service.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	service.randomStep = func(n int) int { return n }
	return service
}

func capSet(caps []authz.Capability) map[authz.Capability]bool {
	m := make(map[authz.Capability]bool, len(caps))
	for _, capability := range caps {
		m[capability] = true
	}
	return m
}

func serialDef() SerialNumber {
	return SerialNumber{
		ID:              1,
		BusinessName:    "Order",
		Format:          "OD[yyyy][nnnn]",
		RuleType:        SerialRuleNone,
		InitNumber:      1000,
		StepRandomRange: 1,
	}
}

func TestSerialSequentialGeneration(t *testing.T) {
	store := &fakeSerialStore{definition: serialDef(), records: map[uint64][]SerialNumberRecord{}}
	service := serialService(store, authz.CapabilitySupportSerialGenerate)
	numbers, err := service.Generate(serialCtx(authz.CapabilitySupportSerialGenerate), SerialNumberGenerateInput{SerialNumberID: 1, Count: 3})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(numbers) != 3 {
		t.Fatalf("count=%d", len(numbers))
	}
	want := []string{"OD20261001", "OD20261002", "OD20261003"}
	for i := range want {
		if numbers[i] != want[i] {
			t.Fatalf("numbers=%v want %v", numbers, want)
		}
	}
	if !store.advanced {
		t.Fatal("store was not advanced")
	}
}

func TestSerialPeriodReset(t *testing.T) {
	last := int64(5000)
	pastYear := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		rule  string
		last  time.Time
		reset bool
	}{
		{"year boundary", SerialRuleYear, pastYear, true},
		{"month boundary", SerialRuleMonth, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), true},
		{"day boundary", SerialRuleDay, time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), true},
		{"same day no reset", SerialRuleDay, time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC), false},
		{"none never resets", SerialRuleNone, pastYear, false},
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := serialDef()
			def.RuleType = tc.rule
			def.LastNumber = &last
			def.LastTime = &tc.last
			got := periodReset(def, now)
			if got != tc.reset {
				t.Fatalf("periodReset=%v want %v", got, tc.reset)
			}
		})
	}
}

func TestSerialRandomStep(t *testing.T) {
	def := serialDef()
	stepSeen := 0
	service := NewSerialNumberService(&fakeSerialStore{definition: def}, dictCapAuthorizer{allowed: capSet([]authz.Capability{authz.CapabilitySupportSerialGenerate})})
	service.now = func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }
	service.randomStep = func(n int) int {
		stepSeen = n
		return n
	}
	def.StepRandomRange = 5
	store := &fakeSerialStore{definition: def}
	service.store = store
	numbers, err := service.Generate(serialCtx(authz.CapabilitySupportSerialGenerate), SerialNumberGenerateInput{SerialNumberID: 1, Count: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if stepSeen != 5 {
		t.Fatalf("random upper bound=%d want 5", stepSeen)
	}
	if numbers[0] != "OD20261005" {
		t.Fatalf("number=%s want OD20261005 (init 1000 + step 5 = 1005 + 1 padding)", numbers[0])
	}
}

func TestSerialFormatVariants(t *testing.T) {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		format string
		number int64
		want   string
	}{
		{"OD[yyyy][nnnn]", 42, "OD20260042"},
		{"INV[yyyy][mm][dd]-[nnn]", 7, "INV20260305-007"},
		{"NO[n]", 12, "NO12"},
	}
	for _, tc := range cases {
		token, digits, y, m, d := parseSerialFormat(tc.format)
		got := formatSerialFormat(tc.format, token, digits, y, m, d, now, tc.number)
		if got != tc.want {
			t.Fatalf("format(%q,%d)=%q want %q", tc.format, tc.number, got, tc.want)
		}
	}
}

// formatSerialFormat is a small test-local alias matching service output shape.
func formatSerialFormat(format, token string, digitCount int, hasYear, hasMonth, hasDay bool, now time.Time, number int64) string {
	return formatSerialNumber(format, token, digitCount, hasYear, hasMonth, hasDay, now, number)
}

func TestSerialValidationAndAuthorization(t *testing.T) {
	store := &fakeSerialStore{definition: serialDef(), records: map[uint64][]SerialNumberRecord{}}
	readOnly := serialService(store, authz.CapabilitySupportSerialRead)
	if _, err := readOnly.Generate(serialCtx(authz.CapabilitySupportSerialRead), SerialNumberGenerateInput{SerialNumberID: 1, Count: 1}); err == nil {
		t.Fatal("generate with read-only capability accepted")
	}
	service := serialService(store, authz.CapabilitySupportSerialGenerate, authz.CapabilitySupportSerialRead)
	for _, input := range []SerialNumberGenerateInput{
		{SerialNumberID: 0, Count: 1},
		{SerialNumberID: 1, Count: 0},
		{SerialNumberID: 1, Count: 1001},
	} {
		if _, err := service.Generate(serialCtx(authz.CapabilitySupportSerialGenerate), input); err == nil || err.Code != coreerrors.CodeInvalidArgument {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	if _, err := service.ListRecords(serialCtx(authz.CapabilitySupportSerialRead), 0, 1, 10); err == nil {
		t.Fatal("record query without id accepted")
	}
}

func TestSerialStorageFailure(t *testing.T) {
	service := serialService(&fakeSerialStore{fail: errors.New("db down")}, authz.CapabilitySupportSerialGenerate)
	if _, err := service.Generate(serialCtx(authz.CapabilitySupportSerialGenerate), SerialNumberGenerateInput{SerialNumberID: 1, Count: 1}); err == nil || err.Code != coreerrors.CodeUnavailable {
		t.Fatalf("err=%v want unavailable", err)
	}
}
