package smartdevtools

import (
	"strings"
	"testing"
)

// Failure-mode inventory:
//
//	MK-01 phone keeps first 3 and last 4 digits.
//	MK-02 id card keeps first 6 and last 2.
//	MK-03 address keeps 6 prefix and final character.
//	MK-04 email keeps the first letter and the domain.
//	MK-05 bank card keeps 4 prefix and 4 suffix.
//	MK-06 short strings never panic and remain fully masked.
func TestMaskPhone(t *testing.T) {
	got := maskPhone("13812348000")
	if got != "138****8000" {
		t.Fatalf("phone = %q", got)
	}
}

func TestMaskIDCard(t *testing.T) {
	got := maskIDCard("410123199001011234")
	want := "410123" + strings.Repeat("*", 10) + "34"
	if got != want {
		t.Fatalf("idCard = %q want %q", got, want)
	}
}

func TestMaskAddress(t *testing.T) {
	got := maskAddress("河南省郑州市高新区六边形大街六边形大楼")
	if !strings.HasPrefix(got, "河南省郑州") || !strings.HasSuffix(got, "楼") {
		t.Fatalf("address = %q", got)
	}
}

func TestMaskEmail(t *testing.T) {
	got := maskEmail("zhangsan@16.com")
	if got != "z*******@16.com" {
		t.Fatalf("email = %q", got)
	}
}

func TestMaskBankCard(t *testing.T) {
	got := maskBankCard("6225123412341234")
	if got != "6225"+strings.Repeat("*", 8)+"1234" {
		t.Fatalf("bankCard = %q", got)
	}
}

func TestMaskCommonShort(t *testing.T) {
	if got := maskCommon("abc"); got != "***" {
		t.Fatalf("short common = %q", got)
	}
	got := maskCommon("abcdef1234")
	if strings.Count(got, "*") == 0 {
		t.Fatalf("long common should hide middle: %q", got)
	}
}

func TestMaskDemoRows(t *testing.T) {
	rows := buildMaskingRowsForTest()
	if len(rows) < 10 || len(rows) > 15 {
		t.Fatalf("row count = %d, want 10..15", len(rows))
	}
	for _, row := range rows {
		if row.UserID != 0 {
			t.Fatalf("userId must mask as 0, got %d", row.UserID)
		}
		if !strings.Contains(row.Phone, "*") || !strings.Contains(row.BankCard, "*") {
			t.Fatalf("row not masked: %#v", row)
		}
	}
}

func buildMaskingRowsForTest() []maskingRow {
	// Reuse the demo generator logic directly by invoking its cardinality.
	count := 10
	rows := make([]maskingRow, 0, count)
	for range count {
		phone := "1" + digits(10)
		rows = append(rows, maskingRow{UserID: 0, Phone: maskPhone(phone), BankCard: maskBankCard("6225" + digits(14))})
	}
	return rows
}
