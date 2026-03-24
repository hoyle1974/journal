package agent

import (
	"math"
	"testing"
)

func TestApplyDecayZeroDays(t *testing.T) {
	got := applyDecay(0.8, 0)
	if math.Abs(got-0.8) > 0.0001 {
		t.Errorf("applyDecay(0.8, 0) = %v, want ~0.8", got)
	}
}

func TestApplyDecayOneDay(t *testing.T) {
	// exp(-0.05 * 1) ≈ 0.9512
	got := applyDecay(1.0, 1)
	want := math.Exp(-decayLambda * 1)
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("applyDecay(1.0, 1) = %v, want %v", got, want)
	}
}

func TestApplyDecayTenDays(t *testing.T) {
	// exp(-0.05 * 10) = exp(-0.5) ≈ 0.6065
	got := applyDecay(1.0, 10)
	want := math.Exp(-decayLambda * 10)
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("applyDecay(1.0, 10) = %v, want %v", got, want)
	}
}

func TestApplyDecayLambdaProperty(t *testing.T) {
	// Verify the mathematical property: after 1/λ days, score halves to 1/e.
	halfLife := 1.0 / decayLambda // 20 days
	got := applyDecay(1.0, halfLife)
	wantApprox := 1.0 / math.E // ≈ 0.3679
	if math.Abs(got-wantApprox) > 0.001 {
		t.Errorf("applyDecay(1.0, 1/lambda) = %v, want ~%v (1/e)", got, wantApprox)
	}
}

func TestApplyDecayNeverNegative(t *testing.T) {
	got := applyDecay(0.001, 1000)
	if got < 0 {
		t.Errorf("applyDecay returned negative score: %v", got)
	}
}
