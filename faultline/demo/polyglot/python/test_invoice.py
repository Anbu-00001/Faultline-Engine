"""Covers invoice_total only — the rate chain it depends on is untested, so a
change to BaseRate.rate has an untested blast radius (this is the point).

Python/pytest test convention: a ``test_*.py`` filename and ``test_*`` functions.
"""

from rates import invoice_total


def test_invoice_total():
    assert invoice_total(100) > 100
