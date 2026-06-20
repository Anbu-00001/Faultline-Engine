// Package rates is the Go arm of Faultline's polyglot demo: a tax-rate chain
// (BaseRate -> StandardRate via struct embedding) plus an InvoiceTotal caller.
//
// Orbit indexes this with CALLS edges (InvoiceTotal -> Rate, StandardRate.Rate ->
// BaseRate.Rate) and an EXTENDS edge (StandardRate embeds BaseRate). Changing
// BaseRate.Rate therefore ripples to StandardRate.Rate and InvoiceTotal — the
// blast radius Faultline computes. Only InvoiceTotal is tested (see
// invoice_test.go), so the rate chain is an *untested* blast radius.
package rates

// BaseRate is the base of the rate hierarchy.
type BaseRate struct{}

// Rate is the base tax rate. A one-line change here is the demo's "merge request".
func (BaseRate) Rate() float64 { return 0.0 }

// StandardRate adds the standard surcharge on top of the base rate.
type StandardRate struct{ BaseRate }

// Rate overrides BaseRate.Rate, calling through to it.
func (s StandardRate) Rate() float64 { return s.BaseRate.Rate() + 0.07 }

// InvoiceTotal applies the standard rate to an amount. It transitively depends
// on BaseRate.Rate, two hops away.
func InvoiceTotal(amount float64) float64 {
	return amount * (1 + StandardRate{}.Rate())
}
