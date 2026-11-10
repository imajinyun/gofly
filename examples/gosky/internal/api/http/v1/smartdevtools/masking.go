// Package smartdevtools exposes the remaining unmodified SmartAdmin
// development-tool demo contracts: data masking demo, process
// heartbeats, change logs, business data tracer, and read-only code
// generator compatibility stubs.
package smartdevtools

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// maskingRow mirrors AdminDataMaskingDemoController.DataVO.
type maskingRow struct {
	UserID     uint64 `json:"userId"`
	Phone      string `json:"phone"`
	IDCard     string `json:"idCard"`
	Address    string `json:"address"`
	Password   string `json:"password"`
	Email      string `json:"email"`
	CarLicense string `json:"carLicense"`
	BankCard   string `json:"bankCard"`
	Other      string `json:"other"`
}

const (
	maskStar   = "*"
	addressesA = "河南省洛阳市洛龙区一零二四大街1024号"
	addressesB = "河南省郑州市高新区六边形大街六边形大楼"
)

// MaskingDemoHandler serves GET /support/dataMasking/demo/query.
// Java generates random rows and masks fields via serialization
// annotations; gosky generates pre-masked rows directly.
func MaskingDemoHandler(_ *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		count := 10 + rand.Intn(6) // 10..15
		rows := make([]maskingRow, 0, count)
		for range count {
			phone := "1" + digits(10)
			idCard := "410" + digits(3) + number(1980, 2010) + number(10, 12) + number(10, 30) + digits(4)
			address := addressesA
			if rand.Intn(2) == 1 {
				address = addressesB
			}
			email := letters(6+rand.Intn(5)) + "@" + letters(2) + ".com"
			plateLetter := letters(1)
			car := "豫" + strings.ToUpper(plateLetter) + " " + strings.ToUpper(alnum(5))
			rows = append(rows, maskingRow{
				// Hutool userId() masks the entire value as "0".
				UserID:     0,
				Phone:      maskPhone(phone),
				IDCard:     maskIDCard(idCard),
				Address:    maskAddress(address),
				Password:   strings.Repeat(maskStar, 10),
				Email:      maskEmail(email),
				CarLicense: maskCarLicense(car),
				BankCard:   maskBankCard("6225" + digits(14)),
				Other:      maskCommon(alnum(1 + rand.Intn(12))),
			})
		}
		ctx.JSON(http.StatusOK, smartweb.OK(rows))
	}
}

func digits(n int) string {
	const digitsChars = "0123456789"
	builder := strings.Builder{}
	for range n {
		builder.WriteByte(digitsChars[rand.Intn(len(digitsChars))])
	}
	return builder.String()
}

func letters(n int) string {
	const lettersChars = "abcdefghijklmnopqrstuvwxyz"
	builder := strings.Builder{}
	for range n {
		builder.WriteByte(lettersChars[rand.Intn(len(lettersChars))])
	}
	return builder.String()
}

func alnum(n int) string {
	const alnumChars = "abcdefghijklmnopqrstuvwxyz0123456789"
	builder := strings.Builder{}
	for range n {
		builder.WriteByte(alnumChars[rand.Intn(len(alnumChars))])
	}
	return builder.String()
}

func number(min, maxExcl int) string {
	return strconv.Itoa(min + rand.Intn(maxExcl-min))
}

// hide replaces [start,end) runes with *.
func hide(value string, start, end int) string {
	runes := []rune(value)
	if start < 0 {
		start = 0
	}
	if end > len(runes) || end < start {
		end = len(runes)
	}
	for i := start; i < end; i++ {
		runes[i] = '*'
	}
	return string(runes)
}

func maskPhone(phone string) string {
	// Hutool mobilePhone: keep 1..3 and last 4, e.g. 138****8000.
	if len(phone) < 7 {
		return strings.Repeat(maskStar, len(phone))
	}
	return hide(phone, 3, len(phone)-4)
}

func maskIDCard(idCard string) string {
	// Hutool idCardNum(idCard, front=6, end=2).
	if len(idCard) <= 8 {
		return strings.Repeat(maskStar, len(idCard))
	}
	return hide(idCard, 6, len(idCard)-2)
}

func maskAddress(address string) string {
	// Keep the first 6 chars and the final character.
	runes := []rune(address)
	if len(runes) <= 7 {
		return strings.Repeat(maskStar, len(runes))
	}
	return hide(address, 6, len(runes)-1)
}

func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return strings.Repeat(maskStar, len(email))
	}
	return hide(email, 1, at)
}

func maskCarLicense(plate string) string {
	// Hutool carLicense keeps the first 2 and last 1 (new-energy keeps 3/1).
	runes := []rune(plate)
	if len(runes) <= 8 {
		if len(runes) <= 3 {
			return plate
		}
		return hide(plate, 2, len(runes)-1)
	}
	return hide(plate, 3, len(runes)-1)
}

func maskBankCard(card string) string {
	if len(card) <= 8 {
		return strings.Repeat(maskStar, len(card))
	}
	return hide(card, 4, len(card)-4)
}

func maskCommon(value string) string {
	// SmartDataMaskingUtil hides the middle third of long values and all
	// characters when the string is shorter than 4.
	if len(value) < 4 {
		return strings.Repeat(maskStar, len(value))
	}
	length := len(value)
	start := length / 3
	end := length - length/3
	return hide(value, start, end)
}
