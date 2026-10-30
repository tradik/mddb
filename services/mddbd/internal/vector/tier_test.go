package vector

import "testing"

func TestMathTierIsTheActiveTier(t *testing.T) {
	if got := MathTier(); got == "" || got != vectorMathTier() {
		t.Fatalf("MathTier() = %q, active tier %q", got, vectorMathTier())
	}
}
