"""Python arm of Faultline's polyglot demo — the same rate chain as the Go and
Ruby arms.

Orbit indexes this with CALLS edges (invoice_total -> StandardRate.rate,
StandardRate.rate -> BaseRate.rate via ``super()``) and an EXTENDS edge
(StandardRate(BaseRate)). Changing ``BaseRate.rate`` ripples to
``StandardRate.rate`` and ``invoice_total``. Only ``invoice_total`` is tested
(see test_invoice.py), so the rate chain is an *untested* blast radius.
"""


class BaseRate:
    """Base of the rate hierarchy."""

    def rate(self) -> float:
        # The base tax rate. A one-line change here is the demo's "merge request".
        return 0.0


class StandardRate(BaseRate):
    """Adds the standard surcharge on top of the base rate."""

    def rate(self) -> float:
        return super().rate() + 0.07


def invoice_total(amount: float) -> float:
    """Apply the standard rate to an amount — transitively depends on BaseRate.rate."""
    return amount * (1 + StandardRate().rate())
