package hardware_test

import (
	"testing"

	"github.com/zboya/nurvis/internal/hardware"
)

func TestRecommend(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		hw   hardware.Info
		want []string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hardware.Recommend(tt.hw)
			// TODO: update the condition below to compare got with tt.want.
			if true {
				t.Errorf("Recommend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecommendInLocal(t *testing.T) {
	hw, err := hardware.Probe()
	if err != nil {
		t.Fatalf("Probe hardware failed: %v", err)
	}
	t.Logf("Detected hardware: %+v", hw)
	got := hardware.Recommend(hw)
	t.Logf("Recommended models: %v", got)
}
