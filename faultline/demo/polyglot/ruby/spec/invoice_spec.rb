# Covers invoice_total only — the rate chain it depends on is untested, so a
# change to BaseRate#rate has an untested blast radius (this is the point).
#
# Ruby/RSpec test convention: a `_spec.rb` filename under `spec/`.

require_relative '../rates'

RSpec.describe 'invoice_total' do
  it 'applies the standard rate' do
    expect(invoice_total(100)).to be > 100
  end
end
