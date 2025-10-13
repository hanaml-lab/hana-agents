// policies/examples/finance.cedar
permit (principal, action == Action::"invoices.read", resource);

@hold("over-limit")
permit (principal, action == Action::"payments.send", resource)
when { context.amount > 1000 };

@id("within-limit")
permit (principal, action == Action::"payments.send", resource)
when { context.amount <= 1000 };

forbid (principal, action == Action::"payments.send", resource)
when { resource.payee in context.untrusted_payees };

@id("weekend-payments")
@window("Sat-Sun 00:00-23:59 Europe/Berlin")
forbid (principal, action == Action::"payments.send", resource);
