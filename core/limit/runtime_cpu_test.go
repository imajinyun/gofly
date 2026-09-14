package limit

import (
	"testing"
)

func TestRuntimeCPUReaderPermille(t *testing.T) {
	tests := []struct {
		name    string
		samples [][2]float64
		want    []int
	}{
		{name: "first sample is unavailable", samples: [][2]float64{{10, 4}}, want: []int{0}},
		{name: "seventy five percent busy", samples: [][2]float64{{10, 4}, {14, 5}}, want: []int{0, 750}},
		{name: "zero total delta fails open", samples: [][2]float64{{10, 4}, {10, 5}}, want: []int{0, 0}},
		{name: "counter reset fails open", samples: [][2]float64{{10, 4}, {2, 1}}, want: []int{0, 0}},
		{name: "idle counter reset fails open", samples: [][2]float64{{10, 4}, {14, 3}}, want: []int{0, 0}},
		{name: "fully idle interval", samples: [][2]float64{{10, 4}, {14, 8}}, want: []int{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := 0
			reader := newRuntimeCPUReader(func() (float64, float64, bool) {
				current := tt.samples[index]
				index++
				return current[0], current[1], true
			})
			for index, want := range tt.want {
				if got := reader.Permille(); got != want {
					t.Fatalf("sample %d load=%d want=%d", index, got, want)
				}
			}
		})
	}
}

func TestRuntimeCPUReaderInvalidSamplesFailOpen(t *testing.T) {
	reader := newRuntimeCPUReader(func() (float64, float64, bool) { return 0, 0, false })
	if got := reader.Permille(); got != 0 {
		t.Fatalf("invalid sample load=%d want=0", got)
	}
	if got := (*RuntimeCPUReader)(nil).Permille(); got != 0 {
		t.Fatalf("nil reader load=%d want=0", got)
	}
	realReader := NewRuntimeCPUReader()
	for range 2 {
		if got := realReader.Permille(); got < 0 || got > 1000 {
			t.Fatalf("runtime CPU load=%d outside [0,1000]", got)
		}
	}
}
