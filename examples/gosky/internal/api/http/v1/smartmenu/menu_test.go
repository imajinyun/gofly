package smartmenu

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory for the historical batch-delete contract:
//
//  1. SM-COMPAT-01: the unchanged SmartAdmin page calls
//     GET /menu/batchDelete?menuIdList=1,2,3; an unknown parameter name must
//     not turn into a 400 while the UI shows a confirmation dialog.
//  2. SM-COMPAT-02: legacy internal callers using ?ids= (and the earlier
//     ?menuId= alias) keep working during convergence.
//  3. SM-COMPAT-03: empty, duplicated, zero, or non-numeric identifiers are
//     rejected before any service call.
func TestParseMenuIDsAcceptsSmartWebContract(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantIDs []uint64
		wantOK  bool
	}{
		{name: "smart-web menuIdList list", query: "menuIdList=1,2,3", wantIDs: []uint64{1, 2, 3}, wantOK: true},
		{name: "smart-web single deletion", query: "menuIdList=810000021", wantIDs: []uint64{810000021}, wantOK: true},
		{name: "legacy ids alias", query: "ids=4,5", wantIDs: []uint64{4, 5}, wantOK: true},
		{name: "legacy menuId alias", query: "menuId=6", wantIDs: []uint64{6}, wantOK: true},
		{name: "missing identifiers", query: "", wantOK: false},
		{name: "zero identifier", query: "menuIdList=1,0", wantOK: false},
		{name: "duplicate identifier", query: "menuIdList=1,1", wantOK: false},
		{name: "non numeric identifier", query: "menuIdList=1,two", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx := &rest.Context{
				Response: recorder,
				Request:  httptest.NewRequest(http.MethodGet, "/menu/batchDelete?"+tt.query, nil),
			}
			got, ok := parseMenuIDs(ctx)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v; body=%s", ok, tt.wantOK, recorder.Body.String())
			}
			if !tt.wantOK {
				return
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("ids = %#v, want %#v", got, tt.wantIDs)
			}
			for index := range got {
				if got[index] != tt.wantIDs[index] {
					t.Fatalf("ids = %#v, want %#v", got, tt.wantIDs)
				}
			}
		})
	}
}
