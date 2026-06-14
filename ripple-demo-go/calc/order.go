package calc

// TotalWithTax returns the amount plus its tax.
// Calls CalculateTax, giving a 2+ hop chain: TotalWithTax -> CalculateTax -> applyRate.
func TotalWithTax(amount float64) float64 {
	return amount + CalculateTax(amount)
}
