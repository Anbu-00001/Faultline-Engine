# Ruby arm of Faultline's polyglot demo — GitLab's own Rails stack.
#
# Orbit indexes this with CALLS edges (invoice_total -> StandardRate#rate,
# StandardRate#rate -> BaseRate#rate via `super`) and an EXTENDS edge
# (StandardRate < BaseRate). Changing BaseRate#rate ripples to StandardRate#rate
# and invoice_total. Only invoice_total is tested (see spec/invoice_spec.rb), so
# the rate chain is an *untested* blast radius.

# Base of the rate hierarchy.
class BaseRate
  # The base tax rate. A one-line change here is the demo's "merge request".
  def rate
    0.0
  end
end

# Adds the standard surcharge on top of the base rate.
class StandardRate < BaseRate
  def rate
    super + 0.07
  end
end

# Apply the standard rate to an amount — transitively depends on BaseRate#rate.
def invoice_total(amount)
  amount * (1 + StandardRate.new.rate)
end
