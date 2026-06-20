package rates

import "testing"

// TestInvoiceTotal covers InvoiceTotal only. The Rate chain it depends on
// (StandardRate.Rate, BaseRate.Rate) is deliberately left untested, so a change
// to BaseRate.Rate has an *untested* blast radius — exactly what Faultline's
// gate is built to catch. Go's test convention: a `_test.go` suffix.
func TestInvoiceTotal(t *testing.T) {
	if InvoiceTotal(100) <= 100 {
		t.Fatalf("expected the standard rate to be applied")
	}
}
