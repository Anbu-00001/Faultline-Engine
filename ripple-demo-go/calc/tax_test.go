package calc

import "testing"

func TestCalculateTax(t *testing.T) {
	got := CalculateTax(100)
	if got != 18 {
		t.Fatalf("CalculateTax(100) = %v, want 18", got)
	}
}

func TestTotalWithTax(t *testing.T) {
	got := TotalWithTax(100)
	if got != 118 {
		t.Fatalf("TotalWithTax(100) = %v, want 118", got)
	}
}
