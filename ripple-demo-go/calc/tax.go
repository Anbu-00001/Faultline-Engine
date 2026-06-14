package calc

// CalculateTax returns the tax owed on an amount using the standard rate.
func CalculateTax(amount float64) float64 {
	return applyRate(amount, standardRate())
}

func applyRate(amount, rate float64) float64 {
	return amount * rate
}

func standardRate() float64 {
	return 0.18
}

// ApplyDiscount reduces amount by pct percent.
// Intentionally has NO test - used to validate untested-blast-radius detection.
func ApplyDiscount(amount, pct float64) float64 {
	return amount - amount*pct/100
}
