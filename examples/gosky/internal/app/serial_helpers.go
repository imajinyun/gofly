package app

import (
	"crypto/rand"
	"math/big"
	"strconv"
)

// padZero left-pads number to width digits. Numbers longer than width are
// emitted in full rather than truncated.
func padZero(number int64, width int) string {
	digits := strconv.FormatInt(number, 10)
	if len(digits) >= width {
		return digits
	}
	leading := make([]byte, 0, width-len(digits))
	for i := 0; i < width-len(digits); i++ {
		leading = append(leading, '0')
	}
	return string(leading) + digits
}

func itoaWidth(number int64, width int) string {
	return padZero(number, width)
}

// randomIntInclusive returns a cryptographically sourced integer in [1,n].
func randomIntInclusive(n int) int {
	if n <= 1 {
		return 1
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 1
	}
	return int(value.Int64()) + 1
}
